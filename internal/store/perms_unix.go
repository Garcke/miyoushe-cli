//go:build !windows

// Unix 权限实现：目录 0700、文件 0600；组/其他位任何权限都视为不安全。
package store

import (
	"errors"
	"io/fs"
	"os"

	"mihoyo_cli/internal/output"
)

// isReparsePath 在 Unix 上仅识别符号链接。
func isReparsePath(path string, fi fs.FileInfo) bool {
	return fi.Mode()&os.ModeSymlink != 0
}

func hardenFile(path string) error { return os.Chmod(path, 0o600) }

func hardenDir(path string) error { return os.Chmod(path, 0o700) }

// checkFilePrivate 拒绝组/其他用户持有任何权限位的凭据文件。
func checkFilePrivate(path string) *output.Error {
	fi, err := os.Lstat(path)
	if err != nil {
		return output.Err(output.CodeStoreIO, "检查凭据文件权限失败: %v", err)
	}
	if isReparsePath(path, fi) {
		return output.Err(output.CodeStoreTarget, "凭据路径是符号链接，拒绝使用")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return output.Err(output.CodeStorePermission,
			"凭据文件权限 %04o 不安全：组/其他用户可访问，请手动改为 0600", perm)
	}
	return nil
}

// checkDirPrivate 拒绝组/其他用户持有任何权限位的专用目录。
func checkDirPrivate(dir string) *output.Error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return output.Err(output.CodeStoreIO, "检查凭据目录权限失败: %v", err)
	}
	if isReparsePath(dir, fi) {
		return output.Err(output.CodeStoreTarget, "凭据目录是符号链接，拒绝使用")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return output.Err(output.CodeStorePermission,
			"凭据目录权限 %04o 不安全：组/其他用户可访问，请手动改为 0700", perm)
	}
	return nil
}

// syncDir 在原子替换后同步目录项，降低断电丢失风险。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
