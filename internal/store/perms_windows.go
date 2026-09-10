//go:build windows

// Windows 权限实现：以当前用户 SID 限制 DACL，检查 DACL 中是否出现
// Everyone / Authenticated Users / BUILTIN\Users 等宽泛主体。
// 不把 chmod(0600) 误认为 Windows 访问控制。
package store

import (
	"errors"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
	"mihoyo_cli/internal/output"
)

// isReparsePath 识别符号链接与其它 reparse point（junction 等）。
func isReparsePath(path string, fi fs.FileInfo) bool {
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func currentUserSID() (*windows.SID, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

// hardenPath 把 DACL 收紧为：当前用户/SYSTEM/Administrators 完全控制，
// 并标记为保护位（不被父目录继承覆盖）。
func hardenPath(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sddl := "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

func hardenFile(path string) error { return hardenPath(path) }

func hardenDir(path string) error { return hardenPath(path) }

// broadSID 报告 sid 是否是允许访问范围之外的宽泛主体。
func broadSID(sid *windows.SID) bool {
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,             // Everyone
		windows.WinAuthenticatedUserSid, // Authenticated Users
		windows.WinBuiltinUsersSid,      // BUILTIN\Users
		windows.WinAnonymousSid,         // Anonymous
	} {
		wk, err := windows.CreateWellKnownSid(kind)
		if err == nil && sid.Equals(wk) {
			return true
		}
	}
	return false
}

// accessMaskBroad 判断掩码是否授予读/写类访问。
func accessMaskBroad(mask windows.ACCESS_MASK) bool {
	const readWrite = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL |
		windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE
	return mask&readWrite != 0
}

// isPrivatePath 检查 DACL：出现宽泛主体且授予读/写访问即不安全。
func isPrivatePath(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return false, nil
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if broadSID(sid) && accessMaskBroad(ace.Mask) {
			return false, nil
		}
	}
	return true, nil
}

func checkFilePrivate(path string) *output.Error {
	fi, err := os.Lstat(path)
	if err != nil {
		return output.Err(output.CodeStoreIO, "检查凭据文件权限失败: %v", err)
	}
	if isReparsePath(path, fi) {
		return output.Err(output.CodeStoreTarget, "凭据路径是符号链接/reparse point，拒绝使用")
	}
	private, err := isPrivatePath(path)
	if err != nil {
		return output.Err(output.CodeStoreIO, "读取凭据文件 DACL 失败: %v", err)
	}
	if !private {
		return output.Err(output.CodeStorePermission,
			"凭据文件 DACL 对其他本地用户开放，请修复访问控制（仅保留当前用户/SYSTEM/Administrators）")
	}
	return nil
}

func checkDirPrivate(dir string) *output.Error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return output.Err(output.CodeStoreIO, "检查凭据目录权限失败: %v", err)
	}
	if isReparsePath(dir, fi) {
		return output.Err(output.CodeStoreTarget, "凭据目录是符号链接/reparse point，拒绝使用")
	}
	private, err := isPrivatePath(dir)
	if err != nil {
		return output.Err(output.CodeStoreIO, "读取凭据目录 DACL 失败: %v", err)
	}
	if !private {
		return output.Err(output.CodeStorePermission,
			"凭据目录 %s 的 DACL 对其他本地用户开放，请修复访问控制", dir)
	}
	return nil
}

// syncDir 在 Windows 上无目录句柄同步的轻量等价物；原子替换已由
// MoveFileEx(REPLACE_EXISTING) 保证，无需额外处理。
func syncDir(dir string) error { return nil }
