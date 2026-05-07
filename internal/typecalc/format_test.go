package typecalc

import "testing"

func TestCheckFormat_Go(t *testing.T) {
	good := New(KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(LangGo).WithState(StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Go code flagged: %v", fe.Payload)
	}
	bad := New(KindCode, "this is not Go").WithLang(LangGo).WithState(StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Go should fail format check")
	}
}

func TestCheckFormat_TS(t *testing.T) {
	good := New(KindCode, "export const x: number = 1").
		WithLang(LangTypeScript).WithState(StateUncompiled)
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TS flagged: %v", fe.Payload)
	}
	bad := New(KindCode, "this contains nothing TS-ish").
		WithLang(LangTypeScript).WithState(StateUncompiled)
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TS should fail")
	}
}

func TestCheckFormat_TestSuite(t *testing.T) {
	good := New(KindTestSuite, "expect(x).toBe(1)")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good TestSuite flagged: %v", fe.Payload)
	}
	bad := New(KindTestSuite, "no assertions here")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad TestSuite should fail")
	}
}

func TestCheckFormat_Architecture(t *testing.T) {
	good := New(KindArchitecture, "modules:\n- foo\n- bar\nintermediate vars:\n- x\n- y")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Architecture flagged: %v", fe.Payload)
	}
	bad := New(KindArchitecture, "lalala just prose")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Architecture should fail")
	}
}

func TestCheckFormat_Signature(t *testing.T) {
	good := New(KindSignature, "f: (x: number) => string")
	if fe := CheckFormat(good); fe != nil {
		t.Fatalf("good Signature flagged: %v", fe.Payload)
	}
	bad := New(KindSignature, "totally undescribed")
	if fe := CheckFormat(bad); fe == nil {
		t.Fatal("bad Signature should fail (no arrow / colon-type)")
	}
}
