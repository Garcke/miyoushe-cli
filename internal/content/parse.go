// parse.go 实现严格 JSON 解析：拒绝非法 UTF-8、重复键与未知字段。
// encoding/json 默认容忍重复键（后者覆盖前者）与未知字段，而 ContentSpec
// 规范（community-features §4）要求两者都报错，因此先做 token 级预扫描。
package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"mihoyo_cli/internal/output"
)

// utf8BOM 是 UTF-8 字节序标记；携带 BOM 的 JSON 会被 encoding/json 拒绝。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decodeStrict 解析一个完整 JSON 文档到 out：
//  1. 非法 UTF-8 / BOM 报错；
//  2. 任意对象层级的重复键报错；
//  3. 未知字段报错（DisallowUnknownFields）；
//  4. 文档末尾的多余内容报错。
func decodeStrict(data []byte, out any) *output.Error {
	inputErr := func(format string, args ...any) *output.Error {
		return output.Err(output.CodeInputInvalid, format, args...)
	}
	if bytes.HasPrefix(data, utf8BOM) {
		return inputErr("ContentSpec 带 UTF-8 BOM，请去掉后再试")
	}
	if !utf8.Valid(data) {
		return inputErr("ContentSpec 不是合法的 UTF-8 文本")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return inputErr("%s", err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return inputErr("ContentSpec 解析失败: %s", friendlyJSONError(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return inputErr("ContentSpec 文档末尾存在多余内容")
	}
	return nil
}

// friendlyJSONError 把 encoding/json 的错误转成可读消息。
func friendlyJSONError(err error) string {
	return strings.TrimPrefix(err.Error(), "json: ")
}

// rejectDuplicateKeys 用 token 流扫描整个文档，任何对象内出现第二次
// 相同键即报错。
//
// 状态机：对象内的 string token 交替为键与值。frame.expectKey 只在
// 对象层有意义——遇到值（字符串/数字/bool/null/子容器）后回到 true。
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	type frame struct {
		isObject  bool
		keys      map[string]struct{}
		expectKey bool
	}
	stack := []*frame{}
	top := func() *frame { return stack[len(stack)-1] }

	valueComplete := func() {
		if len(stack) > 0 && top().isObject {
			top().expectKey = true
		}
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("JSON 语法错误: %s", friendlyJSONError(err))
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{isObject: true, keys: map[string]struct{}{}, expectKey: true})
			case '[':
				stack = append(stack, &frame{})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("JSON 括号不匹配")
				}
				stack = stack[:len(stack)-1]
				valueComplete()
			}
		case string:
			if len(stack) == 0 {
				continue // 顶层标量：由 decodeStrict 拒绝
			}
			f := top()
			if f.isObject && f.expectKey {
				if _, dup := f.keys[t]; dup {
					return fmt.Errorf("存在重复的 JSON 键 %q", t)
				}
				f.keys[t] = struct{}{}
				f.expectKey = false
			} else {
				valueComplete()
			}
		default:
			// 数字 / bool / null：一个值结束。
			valueComplete()
		}
	}
}
