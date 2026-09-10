package api

import (
	"encoding/json"
	"testing"
)

func TestFlexString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"123"`, "123"},
		{`123`, "123"},
		{`null`, ""},
		{`"v2_abc"`, "v2_abc"},
	}
	for _, c := range cases {
		var f FlexString
		if err := json.Unmarshal([]byte(c.in), &f); err != nil {
			t.Fatalf("Unmarshal(%s): %v", c.in, err)
		}
		if f.String() != c.want {
			t.Errorf("FlexString(%s) = %q, want %q", c.in, f.String(), c.want)
		}
	}
}
