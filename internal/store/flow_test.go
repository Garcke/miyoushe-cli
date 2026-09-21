package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 新旧登录链路的凭据都必须能加载；未知 flow 拒绝。
func TestCredentialsFlows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mys")
	sto := &Store{Dir: dir}
	now := time.Now()

	v1 := NewCredentials("1001", "mid1", "v2_a", "dev", "fp", now)
	if v1.Flow != FlowV1 {
		t.Fatalf("NewCredentials 应默认 FlowV1: %s", v1.Flow)
	}
	if oerr := sto.Save(v1); oerr != nil {
		t.Fatal(oerr)
	}
	got, oerr := sto.Load()
	if oerr != nil || got.Flow != FlowV1 {
		t.Fatalf("FlowV1 应可加载: %+v %+v", oerr, got)
	}

	v2 := NewCredentialsFlow(FlowV2, "1002", "mid2", "v2_b", "dev", "fp", now)
	if oerr := sto.Save(v2); oerr != nil {
		t.Fatal(oerr)
	}
	got, oerr = sto.Load()
	if oerr != nil || got.Flow != FlowV2 || got.Stoken != "v2_b" {
		t.Fatalf("FlowV2 应可加载: %+v %+v", oerr, got)
	}

	bad := NewCredentialsFlow("unknown_flow", "1003", "mid3", "v2_c", "dev", "fp", now)
	if oerr := sto.Save(bad); oerr == nil {
		t.Fatal("未知 flow 应拒绝保存")
	}
}
