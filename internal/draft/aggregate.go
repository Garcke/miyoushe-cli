// aggregate.go 实现草稿列表的跨桶类型回退。
//
// 规则：
//  1. 服务端返回非零 view_type，以服务端值为准（source=response）；
//  2. 服务端缺失或为 0，且该草稿只来自一个查询桶时，以来源桶为有效类型
//     （source=query_bucket）；
//  3. 同一草稿跨多个桶且没有有效服务端类型时，保持 unknown；
//  4. 服务端非零值与来源桶冲突时保留服务端值，并产生结构化 notice；
//  5. 回退不得触发逐条详情请求（本文件是纯函数，不发任何请求）。
//
// 同一草稿出现互相冲突的非零服务端类型时返回 PROTOCOL_MISMATCH，
// 不按遍历顺序任选一个。
package draft

import (
	"fmt"
	"sort"
	"strconv"

	"mihoyo_cli/internal/output"
)

// TypeObservation 是同一条草稿在一次列表响应中的类型观察。
type TypeObservation struct {
	DraftID string
	// Bucket 是来源查询桶（1/2/5）；0 表示来源未知。
	Bucket int
	// Present 表示响应是否实际包含 view_type 字段；
	// 缺失不得当作服务端显式返回 0。
	Present bool
	// ViewType 仅在 Present 为 true 时有意义。
	ViewType int
}

// EffectiveType 是跨桶回退后的结论。
type EffectiveType struct {
	// EffectiveViewType 为 nil 表示无法判定（unknown）。
	EffectiveViewType *int
	// Source 取 response、query_bucket 或 unknown。
	Source string
	// Notices 是结构化提示（英文），如服务端值与桶冲突。
	Notices []string
}

// 有效类型来源常量（与 JSON 契约一致）。
const (
	TypeSourceResponse    = "response"
	TypeSourceQueryBucket = "query_bucket"
	TypeSourceUnknown     = "unknown"
)

// 受支持的查询桶。
func isSupportedBucket(v int) bool { return v == 1 || v == 2 || v == 5 }

// AggregateEffectiveType 汇总同一草稿的全部观察。
// observations 必须属于同一 draftID；混合 ID 会被视为调用方错误并返回 INTERNAL。
func AggregateEffectiveType(draftID string, observations []TypeObservation) (EffectiveType, *output.Error) {
	var serverTypes []int     // 非零服务端值（去重后排序）
	buckets := map[int]bool{} // 出现过的受支持桶
	sawPresentZero := false
	for _, ob := range observations {
		if ob.DraftID != draftID {
			return EffectiveType{}, output.Err(output.CodeInternal,
				"aggregate observations for %s mixed draft ids", draftID)
		}
		if isSupportedBucket(ob.Bucket) {
			buckets[ob.Bucket] = true
		}
		if !ob.Present {
			continue
		}
		if ob.ViewType == 0 {
			sawPresentZero = true
			continue
		}
		dup := false
		for _, v := range serverTypes {
			if v == ob.ViewType {
				dup = true
				break
			}
		}
		if !dup {
			serverTypes = append(serverTypes, ob.ViewType)
		}
	}
	_ = sawPresentZero

	// 规则：冲突的非零服务端类型 → 协议错误。
	if len(serverTypes) > 1 {
		sort.Ints(serverTypes)
		return EffectiveType{}, output.Err(output.CodeProtocolMismatch,
			"draft %s reported conflicting view_type values: %v", draftID, serverTypes)
	}

	if len(serverTypes) == 1 {
		v := serverTypes[0]
		eff := EffectiveType{EffectiveViewType: &v, Source: TypeSourceResponse}
		// 规则 4（2026-09-28 收紧）：同一草稿来自多个桶时，只要其中任一桶
		// 与服务端非零值不一致就提示；互相冲突的多个非零服务端值仍走上方
		// PROTOCOL_MISMATCH。
		if len(buckets) > 0 {
			mismatched := make([]int, 0, len(buckets))
			for b := range buckets {
				if b != v {
					mismatched = append(mismatched, b)
				}
			}
			if len(mismatched) > 0 {
				sort.Ints(mismatched)
				eff.Notices = append(eff.Notices, fmt.Sprintf(
					"draft %s: server view_type %d conflicts with query bucket(s) %s",
					draftID, v, intList(mismatched)))
			}
		}
		return eff, nil
	}

	// 规则 2：无有效服务端类型，但只来自一个受支持桶 → 用桶。
	if len(buckets) == 1 {
		for b := range buckets {
			v := b
			return EffectiveType{EffectiveViewType: &v, Source: TypeSourceQueryBucket}, nil
		}
	}

	// 规则 3：多桶且无有效服务端类型 → unknown。
	return EffectiveType{EffectiveViewType: nil, Source: TypeSourceUnknown}, nil
}

// intList 把升序整数列表格式化为逗号分隔串（用于提示信息）。
func intList(vals []int) string {
	out := ""
	for i, v := range vals {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(v)
	}
	return out
}
