package sshclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coddan/sextant/internal/model"
)

func TestSFTPListReadWrite(t *testing.T) {
	client := dialTestClient(t)
	sf, err := client.OpenSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()

	dir := t.TempDir()
	remote := filepath.Join(dir, "hello.txt")
	if err := sf.WriteFile(remote, []byte("hello sftp"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := sf.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "hello.txt" || files[0].Size != int64(len("hello sftp")) {
		t.Fatalf("list = %+v", files)
	}

	data, err := sf.ReadFile(remote, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello sftp" {
		t.Fatalf("read = %q", data)
	}
}

func TestSFTPMkdirRenameRemove(t *testing.T) {
	client := dialTestClient(t)
	sf, err := client.OpenSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()

	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b", "c")
	if err := sf.Mkdir(sub); err != nil {
		t.Fatal(err)
	}
	st, err := sf.Stat(sub)
	if err != nil || !st.IsDir {
		t.Fatalf("stat = %+v err %v", st, err)
	}

	from := filepath.Join(dir, "one.txt")
	to := filepath.Join(dir, "two.txt")
	if err := sf.WriteFile(from, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sf.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.Stat(to); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := sf.Stat(from); err == nil {
		t.Fatal("old path should be gone")
	}
	if err := sf.Remove(to); err != nil {
		t.Fatal(err)
	}
}

func TestSFTPUploadDownload(t *testing.T) {
	client := dialTestClient(t)
	sf, err := client.OpenSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()

	remoteDir := t.TempDir()
	localDir := t.TempDir()
	localSrc := filepath.Join(localDir, "src.txt")
	content := []byte("transfer me over sftp")
	if err := os.WriteFile(localSrc, content, 0o644); err != nil {
		t.Fatal(err)
	}

	remoteDst := filepath.Join(remoteDir, "dst.txt")
	var progress []int64
	if err := sf.Upload(context.Background(), localSrc, remoteDst, func(p model.TransferProgress) {
		progress = append(progress, p.Done)
	}); err != nil {
		t.Fatal(err)
	}
	_ = progress

	back := filepath.Join(localDir, "back.txt")
	if err := sf.Download(context.Background(), remoteDst, back, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("downloaded = %q", got)
	}
}

func TestSFTPRemoveAllWithProgress(t *testing.T) {
	client := dialTestClient(t)
	sf, err := client.OpenSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()

	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	if err := sf.Mkdir(filepath.Join(tree, "nested")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", filepath.Join("nested", "b.txt")} {
		if err := sf.WriteFile(filepath.Join(tree, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	if err := sf.RemoveAll(context.Background(), tree, func(model.TransferProgress) { calls++ }); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.Stat(tree); err == nil {
		t.Fatal("tree should be gone")
	}
	if calls == 0 {
		t.Fatal("expected progress callbacks")
	}
}

func TestSFTPExternalEditorRoundTrip(t *testing.T) {
	client := dialTestClient(t)
	sf, err := client.OpenSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()

	dir := t.TempDir()
	remote := filepath.Join(dir, "edit.txt")
	if err := sf.WriteFile(remote, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A tiny "editor" that uppercases the file, standing in for $EDITOR.
	editor := filepath.Join(t.TempDir(), "ed.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ntr a-z A-Z < \"$1\" > \"$1.tmp\" && mv \"$1.tmp\" \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err := sf.EditRemote(context.Background(), remote, []string{editor}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected the file to be reported as changed")
	}
	got, err := sf.ReadFile(remote, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "BEFORE" {
		t.Fatalf("edited content = %q", got)
	}
}
