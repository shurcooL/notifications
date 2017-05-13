package fs

import (
	"context"
	"encoding/json"
	"os"
	pathpkg "path"

	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/webdav"
)

// jsonEncodeFile encodes v into file at path, overwriting or creating it.
func jsonEncodeFile(ctx context.Context, fs webdav.FileSystem, path string, v interface{}) error {
	f, err := fs.OpenFile(ctx, path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}

// jsonDecodeFile decodes contents of file at path into v.
func jsonDecodeFile(ctx context.Context, fs webdav.FileSystem, path string, v interface{}) error {
	f, err := vfsutil.Open(ctx, fs, path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

// createEmptyFile creates an empty file at path, creating parent directories if needed.
func createEmptyFile(ctx context.Context, fs webdav.FileSystem, path string) error {
	f, err := vfsutil.Create(ctx, fs, path)
	if os.IsNotExist(err) {
		err = vfsutil.MkdirAll(ctx, fs, pathpkg.Dir(path), 0755)
		if err != nil {
			return err
		}
		f, err = vfsutil.Create(ctx, fs, path)
	}
	if err != nil {
		return err
	}
	_ = f.Close()
	return nil
}
