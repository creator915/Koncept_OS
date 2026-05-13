package core

import "testing"

func TestParseLLMOutput_HappyPath(t *testing.T) {
	expected := SumType{
		{Kind: KindCode, State: StateUncompiled, Lang: LangTypeScript},
		{Kind: KindObstacle},
	}
	raw := "TYPE: Uncompiled<Lang<TypeScript, Code>>\nconst x = 1"
	tv, err := ParseLLMOutput(raw, expected)
	if err != nil {
		t.Fatalf("ParseLLMOutput: %v", err)
	}
	if tv.Kind != KindCode {
		t.Fatalf("kind = %v want Code", tv.Kind)
	}
	if tv.State != StateUncompiled {
		t.Fatalf("state = %v want Uncompiled (folded back from label)", tv.State)
	}
	if tv.Lang != LangTypeScript {
		t.Fatalf("lang = %v want TypeScript", tv.Lang)
	}
	if tv.Payload != "const x = 1" {
		t.Fatalf("payload = %q", tv.Payload)
	}
}

func TestParseLLMOutput_FlatLabel(t *testing.T) {
	expected := SumType{
		{Kind: KindObstacle},
		{Kind: KindCode, State: StateUncompiled},
	}
	tv, err := ParseLLMOutput("TYPE: Obstacle\nstuck", expected)
	if err != nil {
		t.Fatalf("ParseLLMOutput: %v", err)
	}
	if tv.Kind != KindObstacle {
		t.Fatalf("kind = %v", tv.Kind)
	}
}

func TestParseLLMOutput_RejectsUnknown(t *testing.T) {
	expected := SumType{{Kind: KindCode}}
	tv, _ := ParseLLMOutput("TYPE: Unicorn\npayload", expected)
	if tv.Kind != KindFormatError {
		t.Fatalf("expected FormatError, got %v", tv.Kind)
	}
}

func TestParseLLMOutput_RejectsMissingHeader(t *testing.T) {
	tv, _ := ParseLLMOutput("just some text\nno header", SumType{{Kind: KindCode}})
	if tv.Kind != KindFormatError {
		t.Fatalf("expected FormatError, got %v", tv.Kind)
	}
}

func TestSumType_FindKind(t *testing.T) {
	s := SumType{
		{Kind: KindCode, State: StateUncompiled},
		{Kind: KindObstacle},
	}
	if _, ok := s.FindKind(KindCode); !ok {
		t.Fatal("FindKind Code should match")
	}
	if _, ok := s.FindKind(KindTestSuite); ok {
		t.Fatal("FindKind TestSuite should not match")
	}
}
