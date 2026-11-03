package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Interactive restore steps (a login that shows a QR code) for agents: the
// step runs under a pseudo-terminal on the target machine; the code comes
// back as a PNG on this machine, which the agent shows in the chat.

// InteractiveResult is one answer of `hearth recipe tty ... --json`.
type InteractiveResult struct {
	JSON string // the report
	PNG  []byte // the QR code, when one waits for a scan
}

type runStepArgs struct {
	Machine string `json:"machine,omitempty" jsonschema:"the machine (name, alias or SSH host); empty = this one"`
	Key     string `json:"key" jsonschema:"the step key from hearth recipe plan --json (an interactive step with commands)"`
}

type pollStepArgs struct {
	Machine string `json:"machine,omitempty" jsonschema:"the machine the step runs on; empty = this one"`
	Session string `json:"session_id" jsonschema:"session_id from run_interactive_step"`
	Since   string `json:"since,omitempty" jsonschema:"qr_hash already shown: wait for a new code or the end"`
}

type inputStepArgs struct {
	Machine string `json:"machine,omitempty" jsonschema:"the machine the step runs on; empty = this one"`
	Session string `json:"session_id" jsonschema:"session_id from run_interactive_step"`
	Text    string `json:"text" jsonschema:"the answer to type (Enter is added); never a secret value"`
}

func (s *Service) addInteractive(srv *mcp.Server) {
	result := func(r InteractiveResult, err error) (*mcp.CallToolResult, any, error) {
		if err != nil {
			return text("Failed: " + err.Error()), nil, nil
		}
		msg := r.JSON
		var rep struct {
			Status string `json:"status"`
			QRPath string `json:"qr_path"`
		}
		_ = json.Unmarshal([]byte(r.JSON), &rep)
		if rep.Status == "waiting_for_scan" && rep.QRPath != "" {
			msg += "\nNOW, before any other tool call, send the user a message with the code (it expires within minutes; the user " +
				"sees nothing until you write it): the image ![扫码登录](" + rep.QRPath + "), and ask them to scan it with the app the " +
				"step names; do not draw the code as text (chat code blocks space their lines, which makes it unscannable). Only then call poll_interactive_step with " +
				"since = qr_hash; if it returns a new code, show that one at once the same way. The file is removed when the step ends."
		}
		out := text(msg)
		if len(r.PNG) > 0 {
			out.Content = append(out.Content, &mcp.ImageContent{MIMEType: "image/png", Data: r.PNG})
		}
		return out, nil, nil
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_interactive_step",
		Description: "Run an interactive restore step (a login that shows a QR code, e.g. codex-mixin setup) on one of the user's " +
			"machines, in the background under a terminal, without the user leaving the chat. Only steps of hearth recipe plan " +
			"with interactive = true and commands; show the user the command and get a yes first. Returns session_id, status " +
			"(waiting_for_scan, running, succeeded, failed, ...), the QR code as a PNG file (qr_path) and the output's last lines. " +
			"When the command ends the step's check runs and the step is marked done.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a runStepArgs) (*mcp.CallToolResult, any, error) {
		return result(s.Interactive("start", a.Machine, a.Key, ""))
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "poll_interactive_step",
		Annotations: readOnly,
		Description: "Status of an interactive step started with run_interactive_step: waits up to ~25 s for a change (a new QR " +
			"code when the old one expired, the end). Call it again while the status is waiting_for_scan or running.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a pollStepArgs) (*mcp.CallToolResult, any, error) {
		return result(s.Interactive("status", a.Machine, a.Session, a.Since))
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name: "answer_interactive_step",
		Description: "Type a short answer into a running interactive step's terminal (a menu choice, y/n) when its output asks. " +
			"Never type secrets: steps get those from the user's Hearth secrets.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a inputStepArgs) (*mcp.CallToolResult, any, error) {
		if strings.ContainsAny(a.Text, "\r\n") {
			return text("one line only"), nil, nil
		}
		return result(s.Interactive("input", a.Machine, a.Session, a.Text))
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stop_interactive_step",
		Description: "End a running interactive step (the user gave up, or it should start again).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a pollStepArgs) (*mcp.CallToolResult, any, error) {
		return result(s.Interactive("stop", a.Machine, a.Session, ""))
	})
}
