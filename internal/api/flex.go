package api

import (
	"encoding/json"
	"strings"
)

// FlexString 容忍上游把数值字段序列化为 JSON number 或 string：
// aid、uid、draft_id 等标识在不同接口/版本中类型不一致。
type FlexString string

// UnmarshalJSON 同时接受 JSON string 与 number。
func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = FlexString(v)
		return nil
	}
	*f = FlexString(s)
	return nil
}

// String 返回原始字符串形式。
func (f FlexString) String() string { return string(f) }
