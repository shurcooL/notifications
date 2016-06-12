package fs

import (
	"encoding/json"
	"os"
	pathpkg "path"

	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/webdav"
)

// jsonEncodeFile encodes v into file at path, overwriting or creating it.
func jsonEncodeFile(fs webdav.FileSystem, path string, v interface{}) error {
	f, err := vfsutil.Create(fs, path)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(v)
	_ = f.Close()
	return err
}

// jsonDecodeFile decodes contents of file at path into v.
func jsonDecodeFile(fs webdav.FileSystem, path string, v interface{}) error {
	f, err := vfsutil.Open(fs, path)
	if err != nil {
		return err
	}
	err = json.NewDecoder(f).Decode(v)
	_ = f.Close()
	return err
}

// createEmptyFile creates an empty file at path, creating parent directories if needed.
func createEmptyFile(fs webdav.FileSystem, path string) error {
	f, err := vfsutil.Create(fs, path)
	if os.IsNotExist(err) {
		err = vfsutil.MkdirAll(fs, pathpkg.Dir(path), 0755)
		if err != nil {
			return err
		}
		f, err = vfsutil.Create(fs, path)
	}
	if err != nil {
		return err
	}
	_ = f.Close()
	return nil
}
