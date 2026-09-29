package draft

import (
	"testing"

	"mihoyo_cli/internal/output"
)

func TestAggregateEffectiveType(t *testing.T) {
	two, five := 2, 5
	cases := []struct {
		name     string
		obs      []TypeObservation
		wantType *int
		wantSrc  string
		notices  int
	}{
		{
			name:     "服务端非零值优先",
			obs:      []TypeObservation{{DraftID: "d1", Bucket: 5, Present: true, ViewType: 5}},
			wantType: &five, wantSrc: TypeSourceResponse,
		},
		{
			name:     "显式零值单桶回退",
			obs:      []TypeObservation{{DraftID: "d1", Bucket: 2, Present: true, ViewType: 0}},
			wantType: &two, wantSrc: TypeSourceQueryBucket,
		},
		{
			name:     "字段缺失单桶回退",
			obs:      []TypeObservation{{DraftID: "d1", Bucket: 5, Present: false}},
			wantType: &five, wantSrc: TypeSourceQueryBucket,
		},
		{
			name: "缺失且跨多桶保持未知",
			obs: []TypeObservation{
				{DraftID: "d1", Bucket: 1, Present: false},
				{DraftID: "d1", Bucket: 5, Present: false},
			},
			wantType: nil, wantSrc: TypeSourceUnknown,
		},
		{
			name: "显式零且跨多桶保持未知",
			obs: []TypeObservation{
				{DraftID: "d1", Bucket: 2, Present: true, ViewType: 0},
				{DraftID: "d1", Bucket: 5, Present: true, ViewType: 0},
			},
			wantType: nil, wantSrc: TypeSourceUnknown,
		},
		{
			name:     "服务端值与桶一致无提示",
			obs:      []TypeObservation{{DraftID: "d1", Bucket: 5, Present: true, ViewType: 5}},
			wantType: &five, wantSrc: TypeSourceResponse,
		},
		{
			name: "服务端值与桶冲突保留服务端值并提示",
			obs: []TypeObservation{
				{DraftID: "d1", Bucket: 2, Present: true, ViewType: 5},
			},
			wantType: &five, wantSrc: TypeSourceResponse, notices: 1,
		},
		{
			name: "重复一致观察",
			obs: []TypeObservation{
				{DraftID: "d1", Bucket: 5, Present: true, ViewType: 5},
				{DraftID: "d1", Bucket: 5, Present: true, ViewType: 5},
			},
			wantType: &five, wantSrc: TypeSourceResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, oerr := AggregateEffectiveType("d1", tc.obs)
			if oerr != nil {
				t.Fatalf("Aggregate: %v", oerr)
			}
			switch {
			case tc.wantType == nil && got.EffectiveViewType != nil:
				t.Fatalf("应为未知，得到 %d", *got.EffectiveViewType)
			case tc.wantType != nil && got.EffectiveViewType == nil:
				t.Fatalf("应得到 %d，实际未知", *tc.wantType)
			case tc.wantType != nil && *got.EffectiveViewType != *tc.wantType:
				t.Fatalf("值 = %d, want %d", *got.EffectiveViewType, *tc.wantType)
			}
			if got.Source != tc.wantSrc {
				t.Errorf("source = %s, want %s", got.Source, tc.wantSrc)
			}
			if len(got.Notices) != tc.notices {
				t.Errorf("notices = %v, want %d 条", got.Notices, tc.notices)
			}
		})
	}
}

func TestAggregateEffectiveType_ConflictingServerValues(t *testing.T) {
	// 同一 ID 出现互相冲突的非零服务端类型 → PROTOCOL_MISMATCH，不按顺序任选。
	_, oerr := AggregateEffectiveType("d1", []TypeObservation{
		{DraftID: "d1", Bucket: 2, Present: true, ViewType: 2},
		{DraftID: "d1", Bucket: 5, Present: true, ViewType: 5},
	})
	if oerr == nil || oerr.Code != output.CodeProtocolMismatch {
		t.Fatalf("冲突类型应 PROTOCOL_MISMATCH: %v", oerr)
	}
}

func TestAggregateEffectiveType_MixedIDsIsInternalError(t *testing.T) {
	_, oerr := AggregateEffectiveType("d1", []TypeObservation{{DraftID: "d2", Bucket: 1}})
	if oerr == nil || oerr.Code != output.CodeInternal {
		t.Fatalf("混合 ID 应 INTERNAL: %v", oerr)
	}
}
