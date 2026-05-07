package lang

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

func TestCheckFormat_Go(t *testing.T) {
	good := typecalc.New(typecalc.KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(typecalc.LangGo).WithState(typecalc.StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Go code flagged: %v", fe.Payload)
	}
	bad := typecalc.New(typecalc.KindCode, "this is not Go").WithLang(typecalc.LangGo).WithState(typecalc.StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Go should fail format check")
	}
}

func TestCheckFormat_TS(t *testing.T) {
	good := typecalc.New(typecalc.KindCode, "export const x: number = 1").
		WithLang(typecalc.LangTypeScript).WithState(typecalc.StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TS flagged: %v", fe.Payload)
	}
	bad := typecalc.New(typecalc.KindCode, "this contains nothing TS-ish").
		WithLang(typecalc.LangTypeScript).WithState(typecalc.StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TS should fail")
	}
}

func TestCheckFormat_TestSuite(t *testing.T) {
	good := typecalc.New(typecalc.KindTestSuite, "expect(x).toBe(1)")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TestSuite flagged: %v", fe.Payload)
	}
	bad := typecalc.New(typecalc.KindTestSuite, "no assertions here")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TestSuite should fail")
	}
}

func TestCheckFormat_Architecture(t *testing.T) {
	good := typecalc.New(typecalc.KindArchitecture, "modules:\n- foo\n- bar\nintermediate vars:\n- x\n- y")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Architecture flagged: %v", fe.Payload)
	}
	bad := typecalc.New(typecalc.KindArchitecture, "lalala just prose")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Architecture should fail")
	}
}

func TestCheckFormat_Signature(t *testing.T) {
	good := typecalc.New(typecalc.KindSignature, "f: (x: number) => string")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Signature flagged: %v", fe.Payload)
	}
	bad := typecalc.New(typecalc.KindSignature, "totally undescribed")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Signature should fail (no arrow / colon-type)")
	}
}
