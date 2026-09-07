package toolchain

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Keep a private, complete pre-install archive in addition to transactional
// rollback. Only owned trees already validated for this transaction are copied.
func (m *Manager) backupInstallation(changes []*replacement) error {
	hasOld := false
	for _, c := range changes {
		hasOld = hasOld || c.Original
	}
	if !hasOld {
		return nil
	}
	root := m.config.StateDir + ".backups"
	if noSymlinks(root) != nil {
		return errors.New("backup_path_unavailable")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return errors.New("backup_unavailable")
	}
	f, e := os.CreateTemp(root, "installation-*.zip")
	if e != nil {
		return errors.New("backup_unavailable")
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()
	z := zip.NewWriter(f)
	for _, c := range changes {
		if !c.Original {
			continue
		}
		prefix := "skills/" + filepath.Base(c.Target)
		if c.Target == m.config.StateDir {
			prefix = "toolchain"
		}
		e = filepath.WalkDir(c.Target, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("backup_symlink")
			}
			relative, _ := filepath.Rel(c.Target, p)
			name := prefix
			if relative != "." {
				name += "/" + filepath.ToSlash(relative)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name = name
			if d.IsDir() {
				h.Name = strings.TrimSuffix(name, "/") + "/"
			}
			w, err := z.CreateHeader(h)
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			input, err := os.Open(p)
			if err != nil {
				return err
			}
			defer input.Close()
			_, err = io.Copy(w, input)
			return err
		})
		if e != nil {
			z.Close()
			return errors.New("backup_failed")
		}
		if !sameTreeSnapshot(c.Target, c.OldFiles, c.OldDirectories) {
			z.Close()
			return errors.New("backup_source_changed")
		}
	}
	if z.Close() != nil || f.Sync() != nil {
		return errors.New("backup_failed")
	}
	ok = true
	return nil
}
