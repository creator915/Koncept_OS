package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/chat"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/tools"
)

const maxIterations = 25

func RunTurn(ctx context.Context, client *llm.Client, messages *[]chat.Message, userPrompt string) error {
	ensureSystem(messages)
	*messages = append(*messages, chat.Message{Role: "user", Content: userPrompt})

	builtins := tools.Builtins()
	specs := tools.Specs(builtins)

	for i := 0; i < maxIterations; i++ {
		var (
			reasoningStarted bool
			contentStarted   bool
		)
		handler := llm.StreamHandler{
			OnReasoning: func(s string) {
				if !reasoningStarted {
					fmt.Fprint(os.Stderr, "\x1b[2m[thinking]\n")
					reasoningStarted = true
				}
				fmt.Fprint(os.Stderr, s)
			},
			OnContent: func(s string) {
				if reasoningStarted && !contentStarted {
					fmt.Fprint(os.Stderr, "\x1b[0m\n")
				}
				contentStarted = true
				fmt.Print(s)
			},
		}

		assistant, err := client.Chat(ctx, *messages, specs, handler)
		if reasoningStarted && !contentStarted {
			fmt.Fprint(os.Stderr, "\x1b[0m\n")
		}
		if err != nil {
			fmt.Println()
			return err
		}
		if contentStarted {
			fmt.Println()
		}
		*messages = append(*messages, *assistant)

		if len(assistant.ToolCalls) == 0 {
			return nil
		}

		for _, tc := range assistant.ToolCalls {
			fmt.Fprintf(os.Stderr, "» %s(%s)\n", tc.Function.Name, truncate(tc.Function.Arguments, 200))
			result := tools.Execute(ctx, builtins, tc.Function.Name, tc.Function.Arguments)
			*messages = append(*messages, chat.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}
	return fmt.Errorf("agent exceeded max iterations (%d)", maxIterations)
}

func ensureSystem(messages *[]chat.Message) {
	if len(*messages) == 0 || (*messages)[0].Role != "system" {
		*messages = append([]chat.Message{{Role: "system", Content: SystemPrompt}}, (*messages)...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
