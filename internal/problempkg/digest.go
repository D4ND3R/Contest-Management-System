package problempkg

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// DigestFS is the SHA-256 of a package directory's content: the paths and
// bytes of the files ReadFS reads, independent of timestamps and order.
// Two packages with the same digest import to the same task.
func DigestFS(fsys fs.FS) (string, error) {
	entries, _, err := fsEntries(fsys)
	if err != nil {
		return "", err
	}
	return digestEntries(entries)
}

// DigestZip is DigestFS for a zipped package (as Export writes it).
func DigestZip(zr *zip.Reader) (string, error) {
	var entries []entry
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || ignored(zf.Name) || hidden(zf.Name) {
			continue
		}
		entries = append(entries, entry{name: zf.Name, src: source{zf: zf}})
	}
	return digestEntries(entries)
}

func hidden(name string) bool {
	for _, part := range strings.Split(path.Clean(name), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func digestEntries(entries []entry) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		rd, err := e.src.open()
		if err != nil {
			return "", err
		}
		fh := sha256.New()
		_, err = io.Copy(fh, rd)
		rd.Close()
		if err != nil {
			return "", err
		}
		io.WriteString(h, e.name+"\x00"+hex.EncodeToString(fh.Sum(nil))+"\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
