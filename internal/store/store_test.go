package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: filepath.Join(t.TempDir(), "mys")}
}

func sampleCreds() *Credentials {
	return NewCredentials("100024680", "mid_abcdef", "v2_synthetic_test_token", "device-syn", "fp0123456789a", time.Now())
}

func TestSaveLoadRoundtrip(t *testing.T) {
	st := newTestStore(t)
	creds := sampleCreds()
	if err := st.Save(creds); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load 返回 nil")
	}
	if got.UID != creds.UID || got.MID != creds.MID || got.Stoken != creds.Stoken ||
		got.DeviceID != creds.DeviceID || got.DeviceFP != creds.DeviceFP ||
		got.TokenKind != "stoken" || got.TokenType != 1 || got.Flow != FlowV1 {
		t.Errorf("roundtrip 不一致: %+v", got)
	}
	if got.ExpiresAt != nil {
		t.Errorf("expires_at 应为 null: %v", *got.ExpiresAt)
	}
}

func TestSave_JSONShape(t *testing.T) {
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema_version", "flow", "uid", "mid", "token_kind",
		"token_type", "stoken", "device_id", "device_fp", "saved_at", "expires_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少字段 %s", k)
		}
	}
	if len(m) != 11 {
		t.Errorf("凭据文件出现意外字段: %v", m)
	}
	if m["expires_at"] != nil {
		t.Errorf("expires_at 应为 null: %v", m["expires_at"])
	}
	// 秘密不落盘：Game Token 级别的合成串之外不应有额外 token 字段。
	if _, ok := m["game_token"]; ok {
		t.Error("凭据文件不应包含 game_token")
	}
}

func TestLoad_Missing(t *testing.T) {
	st := newTestStore(t)
	creds, err := st.Load()
	if err != nil || creds != nil {
		t.Errorf("缺失文件应返回 (nil,nil): %v %v", creds, err)
	}
}

func TestLoad_Corrupt(t *testing.T) {
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(st.Path(), []byte("{not json"), 0o600)
	if _, err := st.Load(); err == nil || err.Code != "STORE_CORRUPT" {
		t.Fatalf("损坏文件应返回 STORE_CORRUPT: %v", err)
	}
	// 校验失败：schema_version 升级。
	os.WriteFile(st.Path(), []byte(`{"schema_version":99}`), 0o600)
	if _, err := st.Load(); err == nil || err.Code != "STORE_CORRUPT" {
		t.Fatalf("未知 schema 应返回 STORE_CORRUPT: %v", err)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	st := newTestStore(t)
	existed, err := st.Delete()
	if err != nil || existed {
		t.Fatalf("幂等删除: %v %v", existed, err)
	}
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	existed, err = st.Delete()
	if err != nil || !existed {
		t.Fatalf("删除: %v %v", existed, err)
	}
	if _, err := os.Lstat(st.Path()); err == nil {
		t.Error("删除后文件仍存在")
	}
}

func TestCheckPermissions_AfterSave(t *testing.T) {
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckPermissions(); err != nil {
		t.Fatalf("保存后的权限应安全: %v", err)
	}
}

// Unix 专属：权限位与符号链接行为可直接构造。
func TestUnixUnsafePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix 权限位测试")
	}
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	os.Chmod(st.Path(), 0o644)
	if err := st.CheckPermissions(); err == nil || err.Code != "STORE_PERMISSION_UNSAFE" {
		t.Fatalf("0644 应判为不安全: %v", err)
	}
	os.Chmod(st.Dir, 0o755)
	if err := st.CheckPermissions(); err == nil || err.Code != "STORE_PERMISSION_UNSAFE" {
		t.Fatalf("0755 目录应判为不安全: %v", err)
	}
}

func TestUnixUnsafeSaveTerminates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix 权限位测试")
	}
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	old := st.Path()
	dataBefore, _ := os.ReadFile(old)
	os.Chmod(st.Path(), 0o666)
	// 已有文件不安全时终止，不覆盖。
	if err := st.Save(NewCredentials("2", "m", "s", "d", "f", time.Now())); err == nil {
		t.Fatal("不安全旧文件应终止保存")
	}
	dataAfter, _ := os.ReadFile(old)
	if string(dataBefore) != string(dataAfter) {
		t.Error("失败保存不应改动旧文件")
	}
}

func TestSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 创建符号链接需要特权")
	}
	st := newTestStore(t)
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	os.WriteFile(outside, []byte("{}"), 0o600)
	os.Symlink(outside, st.Path())
	if _, err := st.Load(); err == nil || err.Code != "STORE_TARGET_INVALID" {
		t.Fatalf("符号链接凭据应拒绝: %v", err)
	}
	if _, err := st.Delete(); err == nil || err.Code != "STORE_TARGET_INVALID" {
		t.Fatalf("符号链接凭据应拒绝删除: %v", err)
	}
}

func TestSaveFailurePreservesOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无法用 chmod 制造目录不可写")
	}
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	oldData, _ := os.ReadFile(st.Path())
	os.Chmod(st.Dir, 0o500) // 只读目录 → CreateTemp 失败
	defer os.Chmod(st.Dir, 0o700)
	if err := st.Save(NewCredentials("2", "m", "s", "d", "f", time.Now())); err == nil {
		t.Fatal("目录只读时保存应失败")
	}
	after, _ := os.ReadFile(st.Path())
	if string(oldData) != string(after) {
		t.Error("失败保存必须保留旧凭据")
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	st := newTestStore(t)
	if err := st.Save(sampleCreds()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("临时文件残留: %s", e.Name())
		}
	}
}
