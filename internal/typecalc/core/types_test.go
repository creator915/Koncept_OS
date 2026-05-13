package core

import (
	"strings"
	"testing"
)

func TestTypedValue_String(t *testing.T) {
	cases := []struct {
		name string
		tv   *TypedValue
		want string
	}{
		{"plain", New(KindCode, "x"), "Code"},
		{"with lang", New(KindCode, "").WithLang(LangGo), "Lang<Go, Code>"},
		{
			"full stack",
			New(KindCode, "").
				WithLang(LangTypeScript).
				WithState(StateUncompiled).
				WithCaps([]string{"r", "w"}).
				WithChannel("s_root"),
			"Chan<s_root, Permitted<{r,w}, Uncompiled<Lang<TypeScript, Code>>>>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tv.String(); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestTypedValue_TagDispatchKey(t *testing.T) {
	t1 := New(KindCode, "").WithLang(LangGo).WithState(StateCompiled).WithChannel("a")
	t2 := New(KindCode, "different payload").WithLang(LangGo).WithState(StateCompiled).WithChannel("b")
	if t1.Tag() != t2.Tag() {
		t.Fatalf("Tag() should be channel-independent: %v vs %v", t1.Tag(), t2.Tag())
	}
	t3 := t1.WithLang(LangPython)
	if t1.Tag() == t3.Tag() {
		t.Fatalf("changing Lang should change Tag")
	}
}

func TestTypedValue_WithContext_Decodes(t *testing.T) {
	tv := New(KindRequest, "")
	updated, err := tv.WithContext("task", "build a counter")
	if err != nil {
		t.Fatalf("WithContext: %v", err)
	}
	var out string
	ok, err := updated.DecodeContext("task", &out)
	if err != nil {
		t.Fatalf("DecodeContext: %v", err)
	}
	if !ok || out != "build a counter" {
		t.Fatalf("decode mismatch: ok=%v out=%q", ok, out)
	}
	// Original is unchanged (immutability invariant for routed values).
	if _, ok := tv.Context["task"]; ok {
		t.Fatalf("original mutated")
	}
}

func TestLangFromExt(t *testing.T) {
	cases := map[string]Lang{
		"ts":   LangTypeScript,
		".tsx": LangTypeScript,
		"go":   LangGo,
		".py":  LangPython,
		"":     LangNone,
		"foo":  LangNone,
	}
	for in, want := range cases {
		if got := LangFromExt(in); got != want {
			t.Fatalf("LangFromExt(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTag_String_FormatStable(t *testing.T) {
	tag := Tag{Kind: KindCode, State: StateCompiled, Lang: LangGo}
	got := tag.String()
	if !strings.Contains(got, "Compiled") || !strings.Contains(got, "Go") || !strings.Contains(got, "Code") {
		t.Fatalf("Tag.String() incomplete: %q", got)
	}
}
