package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
)

// TestRunBatchConcurrent_ActuallyParallel proves the dispatcher runs
// the batch concurrently — wall time of three 80ms sleeps should be
// ~80ms, not ~240ms.
func TestRunBatchConcurrent_ActuallyParallel(t *testing.T) {
	var counter int32
	tool := toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type:     "function",
			Function: transport.ToolFunction{Name: "slowtool"},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			time.Sleep(80 * time.Millisecond)
			atomic.AddInt32(&counter, 1)
			return "done", nil
		},
	}
	builtins := map[string]toolcall.Tool{"slowtool": tool}
	batch := []transport.ToolCall{
		mkCall("slowtool", `{"object_id":"a"}`, "1"),
		mkCall("slowtool", `{"object_id":"b"}`, "2"),
		mkCall("slowtool", `{"object_id":"c"}`, "3"),
	}
	out := make([]string, 3)
	start := time.Now()
	runBatchConcurrent(context.Background(), RunOptions{}, builtins, batch, out)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected ~80ms (parallel), got %v (likely sequential)", elapsed)
	}
	if atomic.LoadInt32(&counter) != 3 {
		t.Fatalf("expected 3 invocations, got %d", counter)
	}
	for i, r := range out {
		if r != "done" {
			t.Fatalf("slot %d: %q", i, r)
		}
	}
}

// TestRunBatchConcurrent_PreservesResultOrder — even though tools
// finish in unpredictable order, the result slot ordering matches the
// input batch ordering (by index).
func TestRunBatchConcurrent_PreservesResultOrder(t *testing.T) {
	var mu sync.Mutex
	finishOrder := []string{}
	tool := toolcall.Tool{
		Concurrent: true,
		Spec:       transport.ToolSpec{Type: "function", Function: transport.ToolFunction{Name: "ordered"}},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id := args["object_id"].(string)
			// Reverse-order delays: c finishes first, a last.
			delays := map[string]time.Duration{"a": 60, "b": 40, "c": 20}
			time.Sleep(delays[id] * time.Millisecond)
			mu.Lock()
			finishOrder = append(finishOrder, id)
			mu.Unlock()
			return "result-" + id, nil
		},
	}
	builtins := map[string]toolcall.Tool{"ordered": tool}
	batch := []transport.ToolCall{
		mkCall("ordered", `{"object_id":"a"}`, "1"),
		mkCall("ordered", `{"object_id":"b"}`, "2"),
		mkCall("ordered", `{"object_id":"c"}`, "3"),
	}
	out := make([]string, 3)
	runBatchConcurrent(context.Background(), RunOptions{}, builtins, batch, out)

	// Out-of-order finish (c first) is expected.
	if finishOrder[0] != "c" {
		t.Logf("finish order was %v (not asserting strict — just sanity)", finishOrder)
	}
	// But result slots are by INPUT order.
	want := []string{"result-a", "result-b", "result-c"}
	for i, w := range want {
		if out[i] != w {
			t.Fatalf("out[%d]=%q want %q", i, out[i], w)
		}
	}
}

func TestIsConcurrent_DefaultsFalseForUnknown(t *testing.T) {
	builtins := map[string]toolcall.Tool{
		"yes": {Concurrent: true, Spec: transport.ToolSpec{Function: transport.ToolFunction{Name: "yes"}}, Run: nil},
		"no":  {Concurrent: false, Spec: transport.ToolSpec{Function: transport.ToolFunction{Name: "no"}}, Run: nil},
	}
	if !isConcurrent(builtins, "yes") {
		t.Fatal("yes should be concurrent")
	}
	if isConcurrent(builtins, "no") {
		t.Fatal("no should not be concurrent")
	}
	if isConcurrent(builtins, "missing") {
		t.Fatal("unknown tools default to false")
	}
}

func TestBatchKey_PrefersObjectID(t *testing.T) {
	tc := mkCall("typecalc_describe", `{"object_id":"Foo","other":"x"}`, "1")
	k := batchKey(tc)
	if !strings.Contains(k, "object_id=Foo") {
		t.Fatalf("expected object_id-keyed: %s", k)
	}
}

func TestBatchKey_FallsBackToPath(t *testing.T) {
	tc := mkCall("read_file", `{"path":"a.go"}`, "1")
	k := batchKey(tc)
	if !strings.Contains(k, "path=a.go") {
		t.Fatalf("expected path-keyed: %s", k)
	}
}

func TestBatchKey_FullArgsFallback(t *testing.T) {
	tc := mkCall("weird_tool", `{"unknown":"value"}`, "1")
	k := batchKey(tc)
	if !strings.HasPrefix(k, "weird_tool:") {
		t.Fatalf("expected name-prefixed fallback: %s", k)
	}
}

// mkCall is a tiny helper for assembling ToolCall records.
func mkCall(name, args, id string) transport.ToolCall {
	return transport.ToolCall{
		ID:   id,
		Type: "function",
		Function: transport.ToolCallFunction{
			Name:      name,
			Arguments: args,
		},
	}
}

// avoid unused-import warning when test set is small
var _ = json.Marshal
