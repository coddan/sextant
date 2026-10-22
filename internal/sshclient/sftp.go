package sshclient

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

// ErrTransferCancelled is returned when the user stops an upload or download.
var ErrTransferCancelled = errors.New("sftp: transfer cancelled")

// SFTPFile is one directory entry.
type SFTPFile struct {
	Name    string
	Path    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
	IsLink  bool
}

// SFTP is an SFTP session over an SSH connection. Closing it leaves the SSH
// connection alone, which is what "disconnect this SFTP session" means.
type SFTP struct {
	client  *sftp.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

// OpenSFTP starts an SFTP subsystem on a new channel.
func (c *Client) OpenSFTP() (*SFTP, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.RequestSubsystem("sftp"); err != nil {
		_ = session.Close()
		return nil, err
	}
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return &SFTP{client: client, session: session, stdin: stdin, stdout: stdout}, nil
}

// Close ends the SFTP subsystem and releases every remote file handle.
func (s *SFTP) Close() error {
	s.client.Close()
	_ = s.stdin.Close()
	return s.session.Close()
}

// List reads a directory.
func (s *SFTP) List(dir string) ([]SFTPFile, error) {
	entries, err := s.client.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]SFTPFile, 0, len(entries))
	for _, e := range entries {
		f := SFTPFile{
			Name:    e.Name(),
			Path:    path.Join(dir, e.Name()),
			Size:    e.Size(),
			Mode:    e.Mode(),
			ModTime: e.ModTime(),
			IsDir:   e.IsDir(),
			IsLink:  e.Mode()&os.ModeSymlink != 0,
		}
		if f.IsLink {
			if target, err := s.client.ReadLink(f.Path); err == nil {
				// Resolve to tell the UI whether the target is a directory.
				if st, err := s.client.Stat(f.Path); err == nil {
					f.IsDir = st.IsDir()
				}
				f.Name = e.Name()
				_ = target
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Stat returns metadata for one path.
func (s *SFTP) Stat(p string) (SFTPFile, error) {
	st, err := s.client.Stat(p)
	if err != nil {
		return SFTPFile{}, err
	}
	return SFTPFile{Name: path.Base(p), Path: p, Size: st.Size(), Mode: st.Mode(), ModTime: st.ModTime(), IsDir: st.IsDir()}, nil
}

// Mkdir creates a directory, including parents.
func (s *SFTP) Mkdir(p string) error {
	if err := s.client.MkdirAll(p); err != nil {
		return err
	}
	return s.client.Chmod(p, 0o755)
}

// Rename moves a file or directory.
func (s *SFTP) Rename(oldPath, newPath string) error {
	return s.client.Rename(oldPath, newPath)
}

// Remove deletes a file or an empty directory.
func (s *SFTP) Remove(p string) error {
	st, err := s.client.Stat(p)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return s.client.RemoveDirectory(p)
	}
	return s.client.Remove(p)
}

// RemoveAll deletes a tree, reporting progress and allowing cancellation.
// Entries are removed one by one so cancellation is checked between them.
func (s *SFTP) RemoveAll(ctx context.Context, root string, progress func(model.TransferProgress)) error {
	p := newProgress(model.Download, "", 0)
	p.Label = "Deleting " + root
	var files []string
	var dirs []string
	err := s.walk(ctx, root, &files, &dirs)
	if err != nil {
		return err
	}
	p.Total = int64(len(files) + len(dirs))
	reportProgress(progress, p, true)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			p.Cancelled = true
			reportProgress(progress, p, true)
			return ErrTransferCancelled
		}
		if err := s.client.Remove(f); err != nil {
			return err
		}
		p.Done++
		p.DoneFiles++
		reportProgress(progress, p, false)
	}
	// Deepest directories first.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			p.Cancelled = true
			reportProgress(progress, p, true)
			return ErrTransferCancelled
		}
		if err := s.client.RemoveDirectory(d); err != nil {
			return err
		}
		p.Done++
		reportProgress(progress, p, false)
	}
	p.Finished = true
	reportProgress(progress, p, true)
	return nil
}

func (s *SFTP) walk(ctx context.Context, dir string, files, dirs *[]string) error {
	if err := ctx.Err(); err != nil {
		return ErrTransferCancelled
	}
	entries, err := s.client.ReadDir(dir)
	if err != nil {
		return err
	}
	*dirs = append(*dirs, dir)
	for _, e := range entries {
		full := path.Join(dir, e.Name())
		if e.IsDir() {
			if err := s.walk(ctx, full, files, dirs); err != nil {
				return err
			}
			continue
		}
		*files = append(*files, full)
	}
	return nil
}

// ReadFile reads up to limit bytes (0 means all). Used by the editor and the
// preview pane; the handle is always closed.
func (s *SFTP) ReadFile(remote string, limit int64) ([]byte, error) {
	f, err := s.client.Open(remote)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if limit <= 0 {
		return io.ReadAll(f)
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// WriteFile replaces a remote file, closing the handle even on error.
func (s *SFTP) WriteFile(remote string, data []byte, mode os.FileMode) error {
	f, err := s.client.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if mode != 0 {
		_ = s.client.Chmod(remote, mode)
	}
	return nil
}

// Download copies one remote file to a local path.
func (s *SFTP) Download(ctx context.Context, remote, local string, progress func(model.TransferProgress)) error {
	src, err := s.client.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer dst.Close()
	p := newProgress(model.Download, remote, st.Size())
	p.Label = path.Base(remote)
	return copyWithProgress(ctx, dst, src, p, progress)
}

// Upload copies one local file to a remote path.
func (s *SFTP) Upload(ctx context.Context, local, remote string, progress func(model.TransferProgress)) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	if err := s.client.MkdirAll(path.Dir(remote)); err != nil {
		return err
	}
	dst, err := s.client.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer dst.Close()
	p := newProgress(model.Upload, remote, st.Size())
	p.Label = filepath.Base(local)
	if err := copyWithProgress(ctx, dst, src, p, progress); err != nil {
		return err
	}
	return s.client.Chmod(remote, st.Mode().Perm())
}

// DownloadDir copies a remote tree into localDir, reporting progress per file.
func (s *SFTP) DownloadDir(ctx context.Context, remote, localDir string, progress func(model.TransferProgress)) error {
	total, files, err := s.remoteTreeSize(ctx, remote)
	if err != nil {
		return err
	}
	p := newProgress(model.Download, remote, total)
	p.Label = path.Base(remote)
	p.Files = len(files)
	reportProgress(progress, p, true)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			p.Cancelled = true
			reportProgress(progress, p, true)
			return ErrTransferCancelled
		}
		rel := strings.TrimPrefix(f.rel, "/")
		target := filepath.Join(localDir, filepath.FromSlash(rel))
		if f.dir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := s.Download(ctx, f.path, target, func(model.TransferProgress) {}); err != nil {
			return err
		}
		p.Done += f.size
		p.DoneFiles++
		if p.Done > p.Total {
			p.Done = p.Total
		}
		reportProgress(progress, p, false)
	}
	p.Finished = true
	p.Done = p.Total
	reportProgress(progress, p, true)
	return nil
}

// UploadDir copies a local tree into remoteDir.
func (s *SFTP) UploadDir(ctx context.Context, localDir, remoteDir string, progress func(model.TransferProgress)) error {
	total, count, err := localTreeSize(localDir)
	if err != nil {
		return err
	}
	p := newProgress(model.Upload, remoteDir, total)
	p.Label = filepath.Base(localDir)
	p.Files = count
	reportProgress(progress, p, true)
	err = filepath.Walk(localDir, func(p2 string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ErrTransferCancelled
		}
		rel, err := filepath.Rel(localDir, p2)
		if err != nil {
			return err
		}
		remotePath := path.Join(remoteDir, filepath.ToSlash(rel))
		if info.IsDir() {
			return s.Mkdir(remotePath)
		}
		if err := s.Upload(ctx, p2, remotePath, func(model.TransferProgress) {}); err != nil {
			return err
		}
		p.Done += info.Size()
		p.DoneFiles++
		if p.Done > p.Total {
			p.Done = p.Total
		}
		reportProgress(progress, p, false)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrTransferCancelled) || ctx.Err() != nil {
			p.Cancelled = true
			reportProgress(progress, p, true)
			return ErrTransferCancelled
		}
		return err
	}
	p.Finished = true
	p.Done = p.Total
	reportProgress(progress, p, true)
	return nil
}

type treeEntry struct {
	path string
	rel  string
	size int64
	dir  bool
}

func (s *SFTP) remoteTreeSize(ctx context.Context, root string) (int64, []treeEntry, error) {
	var total int64
	var files []treeEntry
	err := s.walkEntries(ctx, root, root, &files, &total)
	return total, files, err
}

func (s *SFTP) walkEntries(ctx context.Context, root, dir string, files *[]treeEntry, total *int64) error {
	if err := ctx.Err(); err != nil {
		return ErrTransferCancelled
	}
	entries, err := s.client.ReadDir(dir)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, dir)
	if rel == "." {
		rel = ""
	}
	*files = append(*files, treeEntry{path: dir, rel: rel, dir: true})
	for _, e := range entries {
		full := path.Join(dir, e.Name())
		childRel := path.Join(rel, e.Name())
		if e.IsDir() {
			if err := s.walkEntries(ctx, root, full, files, total); err != nil {
				return err
			}
			continue
		}
		*files = append(*files, treeEntry{path: full, rel: childRel, size: e.Size()})
		*total += e.Size()
	}
	return nil
}

func localTreeSize(dir string) (int64, int, error) {
	var total int64
	count := 0
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
			count++
		}
		return nil
	})
	return total, count, err
}

// EditRemote downloads the file, runs the user's editor on the temporary copy
// and uploads it again if it changed. editorCommand is an argv
// list, so "code --wait" works.
func (s *SFTP) EditRemote(ctx context.Context, remote string, editor []string, progress func(model.TransferProgress)) (bool, error) {
	if len(editor) == 0 {
		return false, errors.New("sftp: no editor configured")
	}
	dir, err := os.MkdirTemp("", "sextant-edit-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	local := filepath.Join(dir, path.Base(remote))
	if err := s.Download(ctx, remote, local, progress); err != nil {
		return false, err
	}
	before, err := os.ReadFile(local)
	if err != nil {
		return false, err
	}
	cmd := exec.CommandContext(ctx, editor[0], append(editor[1:], local)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return false, err
	}
	after, err := os.ReadFile(local)
	if err != nil {
		return false, err
	}
	if string(before) == string(after) {
		return false, nil
	}
	return true, s.WriteFile(remote, after, 0o644)
}

func newProgress(dir model.TransferDirection, label string, total int64) model.TransferProgress {
	p := model.TransferProgress{
		ID:        model.NewID(),
		Label:     label,
		Direction: dir,
		Total:     total,
	}
	return p
}

func reportProgress(cb func(model.TransferProgress), p model.TransferProgress, force bool) {
	if cb == nil {
		return
	}
	if !force && time.Since(lastReport(p.ID)) < 80*time.Millisecond {
		return
	}
	markReported(p.ID)
	cb(p)
}

var progressTimes = struct {
	mu sync.Mutex
	m  map[string]time.Time
}{m: map[string]time.Time{}}

func lastReport(id string) time.Time {
	progressTimes.mu.Lock()
	defer progressTimes.mu.Unlock()
	return progressTimes.m[id]
}

func markReported(id string) {
	progressTimes.mu.Lock()
	defer progressTimes.mu.Unlock()
	progressTimes.m[id] = time.Now()
}

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, p model.TransferProgress, cb func(model.TransferProgress)) error {
	buf := make([]byte, 64*1024)
	reportProgress(cb, p, true)
	for {
		if err := ctx.Err(); err != nil {
			p.Cancelled = true
			reportProgress(cb, p, true)
			return ErrTransferCancelled
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			p.Done += int64(n)
			if p.Total > 0 && p.Done > p.Total {
				p.Done = p.Total
			}
			reportProgress(cb, p, false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	p.Finished = true
	reportProgress(cb, p, true)
	return nil
}
