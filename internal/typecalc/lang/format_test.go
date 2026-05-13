package lang

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestCheckFormat_Go(t *testing.T) {
	good := core.New(core.KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(core.LangGo).WithState(core.StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Go code flagged: %v", fe.Payload)
	}
	bad := core.New(core.KindCode, "this is not Go").WithLang(core.LangGo).WithState(core.StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Go should fail format check")
	}
}

func TestCheckFormat_TS(t *testing.T) {
	good := core.New(core.KindCode, "export const x: number = 1").
		WithLang(core.LangTypeScript).WithState(core.StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TS flagged: %v", fe.Payload)
	}
	bad := core.New(core.KindCode, "this contains nothing TS-ish").
		WithLang(core.LangTypeScript).WithState(core.StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TS should fail")
	}
}

func TestCheckFormat_TestSuite(t *testing.T) {
	good := core.New(core.KindTestSuite, "expect(x).toBe(1)")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TestSuite flagged: %v", fe.Payload)
	}
	bad := core.New(core.KindTestSuite, "no assertions here")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TestSuite should fail")
	}
}

func TestCheckFormat_Architecture(t *testing.T) {
	good := core.New(core.KindArchitecture, "modules:\n- foo\n- bar\nintermediate vars:\n- x\n- y")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Architecture flagged: %v", fe.Payload)
	}
	bad := core.New(core.KindArchitecture, "lalala just prose")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Architecture should fail")
	}
}

func TestCheckFormat_Signature(t *testing.T) {
	good := core.New(core.KindSignature, "f: (x: number) => string")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Signature flagged: %v", fe.Payload)
	}
	bad := core.New(core.KindSignature, "totally undescribed")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Signature should fail (no arrow / colon-type)")
	}
}
