package gui

import (
	"fmt"
	"os"
	"path"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// sftpView is a remote file browser. Closing it disconnects only SFTP and
// leaves the SSH connection up.
type sftpView struct {
	app   *App
	host  model.Host
	sftp  *sshclient.SFTP
	path  string
	files []sshclient.SFTPFile
	sel   int

	pathLabel *widget.Label
	listBox   *fyne.Container
}

func (g *App) buildSFTPTab() fyne.CanvasObject {
	g.sftp = &sftpView{app: g, path: ".", sel: -1}
	g.sftp.pathLabel = widget.NewLabel("/")
	g.sftp.listBox = container.NewVBox()

	parentBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SFTPParentDirectory), theme.NavigateBackIcon(), func() { g.sftp.list(path.Dir(g.sftp.path)) })
	refreshBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonRefresh), theme.ViewRefreshIcon(), func() { g.sftp.list(g.sftp.path) })
	uploadBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonUpload), theme.UploadIcon(), func() { g.sftp.upload() })
	downloadBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonDownload), theme.DownloadIcon(), func() { g.sftp.download() })
	editBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SFTPEdit), theme.DocumentCreateIcon(), func() { g.sftp.edit() })
	mkdirBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SFTPNewFolder), theme.FolderNewIcon(), func() { g.sftp.mkdir() })
	deleteBtn := widget.NewButtonWithIcon(g.tr.T(i18n.CommonDelete), theme.DeleteIcon(), func() { g.sftp.remove() })
	disconnectBtn := widget.NewButtonWithIcon(g.tr.T(i18n.SFTPDisconnect), theme.LogoutIcon(), func() { g.sftp.disconnect() })

	pathRow := container.NewBorder(nil, nil, widget.NewLabel(g.tr.T(i18n.SFTPRemotePath)+":"), nil, g.sftp.pathLabel)
	toolbar := container.NewHBox(parentBtn, refreshBtn, uploadBtn, downloadBtn, editBtn, mkdirBtn, deleteBtn, disconnectBtn)
	hint := widget.NewLabelWithStyle(g.tr.T(i18n.SFTPDragDrop), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	top := container.NewVBox(pathRow, toolbar, hint, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, container.NewVScroll(g.sftp.listBox))
}

// openSFTP connects (or reuses a connection) and opens the browser.
func (g *App) openSFTP(h model.Host) {
	g.showScreen(screenSFTP)
	g.sftp.host = h
	g.sftp.listBox.Objects = nil
	g.sftp.listBox.Refresh()
	g.sftp.pathLabel.SetText("…")
	g.notify(model.NotifyInfo, fmt.Sprintf("%s %s…", g.tr.T(i18n.MetricsCollecting), h.Name))
	go func() {
		client, err := g.connectedClient(g.ctx, h)
		if err == nil {
			var sftp *sshclient.SFTP
			sftp, err = client.OpenSFTP()
			if err == nil {
				fyne.Do(func() {
					g.sftp.sftp = sftp
					g.sftp.path = "."
					g.sftp.list(".")
				})
				return
			}
		}
		fyne.Do(func() {
			g.sftp.pathLabel.SetText("")
			g.showError(friendlyError(g.tr, err))
		})
	}()
}

// list loads a directory in the background.
func (v *sftpView) list(dir string) {
	if v.sftp == nil {
		return
	}
	sftp := v.sftp
	if dir == "" {
		dir = "."
	}
	go func() {
		files, err := sftp.List(dir)
		fyne.Do(func() {
			if err != nil {
				v.app.showError(err)
				return
			}
			sort.SliceStable(files, func(i, j int) bool {
				if files[i].IsDir != files[j].IsDir {
					return files[i].IsDir
				}
				return files[i].Name < files[j].Name
			})
			v.files = files
			v.sel = -1
			v.path = dir
			v.render()
		})
	}()
}

func (v *sftpView) render() {
	v.pathLabel.SetText(v.path)
	box := v.listBox
	box.Objects = nil
	if len(v.files) == 0 {
		empty := widget.NewLabel(v.app.tr.T(i18n.SFTPEmptyDirectory))
		empty.Alignment = fyne.TextAlignCenter
		box.Add(container.NewPadded(empty))
	}
	for i, f := range v.files {
		idx := i
		file := f
		icon := theme.DocumentIcon()
		if file.IsDir {
			icon = theme.FolderIcon()
		}
		label := fmt.Sprintf("%-40s  %12s  %s", truncateName(file.Name, 40), sizeText(file.Size, file.IsDir), file.ModTime.Format("2006-01-02 15:04"))
		btn := widget.NewButtonWithIcon(label, icon, func() {
			v.sel = idx
			if file.IsDir {
				v.list(file.Path)
			} else {
				v.app.notify(model.NotifyInfo, file.Path)
			}
		})
		btn.Alignment = widget.ButtonAlignLeading
		box.Add(btn)
	}
	box.Refresh()
}

func (v *sftpView) selected() (sshclient.SFTPFile, bool) {
	if v.sel < 0 || v.sel >= len(v.files) {
		return sshclient.SFTPFile{}, false
	}
	return v.files[v.sel], true
}

func (v *sftpView) upload() {
	if v.sftp == nil {
		return
	}
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		local := reader.URI().Path()
		_ = reader.Close()
		remote := path.Join(v.path, path.Base(local))
		sftp := v.sftp
		v.app.notify(model.NotifyInfo, v.app.tr.T(i18n.SFTPTransferring, path.Base(local)))
		go func() {
			err := sftp.Upload(v.app.ctx, local, remote, nil)
			fyne.Do(func() {
				if err != nil {
					v.app.showError(err)
					return
				}
				v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.SFTPTransferDone, remote))
				v.list(v.path)
			})
		}()
	}, v.app.win)
}

// uploadDropped uploads files the user dragged from the OS file manager onto the
// window. Directories are copied recursively, mirroring the toolbar's upload
// action. Fyne delivers drops window-wide, so the target is the directory the
// SFTP browser currently shows.
func (v *sftpView) uploadDropped(items []fyne.URI) {
	if v.sftp == nil {
		return
	}
	var locals []string
	for _, item := range items {
		if item == nil {
			continue
		}
		if p := item.Path(); p != "" {
			locals = append(locals, p)
		}
	}
	if len(locals) == 0 {
		return
	}
	sftp := v.sftp
	dir := v.path
	v.app.notify(model.NotifyInfo, v.app.tr.T(i18n.SFTPTransferring, path.Base(locals[0])))
	go func() {
		var firstErr error
		for _, local := range locals {
			remote := path.Join(dir, path.Base(local))
			var err error
			if fi, statErr := os.Stat(local); statErr == nil && fi.IsDir() {
				err = sftp.UploadDir(v.app.ctx, local, remote, nil)
			} else {
				err = sftp.Upload(v.app.ctx, local, remote, nil)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		fyne.Do(func() {
			if firstErr != nil {
				v.app.showError(firstErr)
			} else {
				v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.SFTPTransferDone, dir))
			}
			v.list(v.path)
		})
	}()
}

func (v *sftpView) download() {
	f, ok := v.selected()
	if !ok || f.IsDir {
		dialog.ShowInformation(v.app.tr.T(i18n.CommonDownload), v.app.tr.T(i18n.SFTPDownloadFile), v.app.win)
		return
	}
	dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil || writer == nil {
			return
		}
		local := writer.URI().Path()
		_ = writer.Close()
		sftp := v.sftp
		v.app.notify(model.NotifyInfo, v.app.tr.T(i18n.SFTPTransferring, path.Base(f.Path)))
		go func() {
			err := sftp.Download(v.app.ctx, f.Path, local, nil)
			fyne.Do(func() {
				if err != nil {
					v.app.showError(err)
					return
				}
				v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.SFTPTransferDone, local))
			})
		}()
	}, v.app.win)
}

// edit opens the file in an in-app editor so a change can be saved back without
// leaving the GUI.
func (v *sftpView) edit() {
	f, ok := v.selected()
	if !ok || f.IsDir || v.sftp == nil {
		return
	}
	sftp := v.sftp
	go func() {
		data, err := sftp.ReadFile(f.Path, 4<<20)
		fyne.Do(func() {
			if err != nil {
				v.app.showError(err)
				return
			}
			entry := widget.NewMultiLineEntry()
			entry.SetText(string(data))
			entry.Wrapping = fyne.TextWrapOff
			entry.TextStyle = fyne.TextStyle{Monospace: true}
			entry.SetMinRowsVisible(22)
			d := dialog.NewCustomConfirm(v.app.tr.T(i18n.SFTPEdit), v.app.tr.T(i18n.CommonSave), v.app.tr.T(i18n.CommonCancel), entry, func(save bool) {
				if !save {
					return
				}
				text := entry.Text
				go func() {
					err := sftp.WriteFile(f.Path, []byte(text), f.Mode)
					fyne.Do(func() {
						if err != nil {
							v.app.showError(err)
							return
						}
						v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.NotifySaved))
					})
				}()
			}, v.app.win)
			d.Resize(fyne.NewSize(860, 640))
			d.Show()
		})
	}()
}

func (v *sftpView) mkdir() {
	v.app.askText(v.app.tr.T(i18n.SFTPNewFolder), v.app.tr.T(i18n.SFTPName), "", false, func(name string) {
		if name == "" || v.sftp == nil {
			return
		}
		sftp := v.sftp
		dir := path.Join(v.path, name)
		go func() {
			err := sftp.Mkdir(dir)
			fyne.Do(func() {
				if err != nil {
					v.app.showError(err)
					return
				}
				v.list(v.path)
			})
		}()
	})
}

func (v *sftpView) remove() {
	f, ok := v.selected()
	if !ok {
		return
	}
	dialog.ShowConfirm(v.app.tr.T(i18n.CommonDelete), fmt.Sprintf("%s\n\n%s", f.Path, v.app.tr.T(i18n.SFTPDeleteConfirm)), func(yes bool) {
		if !yes || v.sftp == nil {
			return
		}
		sftp := v.sftp
		go func() {
			var err error
			if f.IsDir {
				err = sftp.RemoveAll(v.app.ctx, f.Path, nil)
			} else {
				err = sftp.Remove(f.Path)
			}
			fyne.Do(func() {
				if err != nil {
					v.app.showError(err)
					return
				}
				v.list(v.path)
			})
		}()
	}, v.app.win)
}

// disconnect closes only the SFTP session.
func (v *sftpView) disconnect() {
	if v.sftp != nil {
		_ = v.sftp.Close()
		v.sftp = nil
	}
	v.files = nil
	v.listBox.Objects = nil
	v.listBox.Refresh()
	v.pathLabel.SetText("")
	v.app.notify(model.NotifySuccess, v.app.tr.T(i18n.NotifyDisconnected))
}

func truncateName(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func sizeText(n int64, isDir bool) string {
	if isDir {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

var _ = time.Now
