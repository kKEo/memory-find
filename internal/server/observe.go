package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memors/internal/chunk"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/obs"
)

// Observability of the MCP surface (roadmap P10): one receiving middleware
// counts every request, times every tool call, classifies failures and
// writes one log line per call. Nothing here leaves the process: metrics are
// scraped from loopback, logs go to stderr.

type mcpMetrics struct {
	requests     *obs.CounterVec
	inFlight     *obs.Gauge
	toolCalls    *obs.CounterVec
	toolSeconds  *obs.HistogramVec
	toolTokens   *obs.HistogramVec
	toolErrors   *obs.CounterVec
	elicitations *obs.CounterVec
	resources    *obs.CounterVec
	sessions     *obs.CounterVec
}

func (s *Server) registerMetrics() {
	r := s.registry
	s.m = &mcpMetrics{
		requests:     r.Counter("memors_mcp_requests_total", "MCP requests received, by method and outcome.", "method", "outcome"),
		inFlight:     r.Gauge("memors_mcp_requests_in_flight", "MCP requests being served right now.").With(),
		toolCalls:    r.Counter("memors_mcp_tool_calls_total", "Tool calls, by tool and outcome (ok, tool_error, input_required, error).", "tool", "outcome"),
		toolSeconds:  r.Histogram("memors_mcp_tool_call_duration_seconds", "Tool call latency.", obs.LatencyBuckets, "tool"),
		toolTokens:   r.Histogram("memors_mcp_tool_result_tokens", "Estimated tokens in a tool result's text.", obs.TokenBuckets, "tool"),
		toolErrors:   r.Counter("memors_mcp_tool_errors_total", "Tool errors by class (not_found, forgotten, needs_human, canceled, invalid_args, internal).", "tool", "class"),
		elicitations: r.Counter("memors_mcp_elicitations_total", "Human questions asked by tools, by outcome (asked, accept, decline, cancel, unsupported).", "tool", "outcome"),
		resources:    r.Counter("memors_mcp_resource_reads_total", "Resource reads, by address kind and outcome.", "kind", "outcome"),
		sessions:     r.Counter("memors_mcp_sessions_total", "Sessions initialised, by client name.", "client"),
	}
}

// callInfo travels from the middleware to the typed handler wrapper and
// back: the wrapper sees the Go error (the SDK turns it into IsError), the
// middleware sees the timing and the wire result.
type callInfo struct {
	tool     string
	err      error
	class    string
	nResults int
	liveID   uint64
}

type callInfoKey struct{}

func infoFrom(ctx context.Context) *callInfo {
	ci, _ := ctx.Value(callInfoKey{}).(*callInfo)
	return ci
}

// observe is the receiving middleware.
func (s *Server) observe(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		t := obs.Start()
		s.m.inFlight.Inc()
		defer s.m.inFlight.Dec()
		var info *callInfo
		if method == "tools/call" {
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
				info = &callInfo{tool: p.Name, liveID: s.live.begin(req, p.Name)}
				defer s.live.end(info.liveID)
				ctx = context.WithValue(ctx, callInfoKey{}, info)
			}
		}
		res, err := next(ctx, method, req)
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		s.m.requests.With(method, outcome).Inc()
		s.live.seen(req)
		switch method {
		case "initialize", "server/discover":
			// Old protocol: initialize; 2026-07-28: discover, with the client
			// identity in request metadata that the session has validated.
			// Only a request that leaves a session behind counts: go-sdk
			// clients probe an HTTP server with server/discover first, on a
			// session the SDK closes right after.
			if err == nil && established(req) {
				s.m.sessions.With(sessionClient(req)).Inc()
				s.live.connected(req)
			}
		case "resources/read":
			kind := "unknown"
			if p, ok := req.GetParams().(*mcp.ReadResourceParams); ok {
				kind = resourceKind(p.URI)
			}
			s.m.resources.With(kind, outcome).Inc()
		case "tools/call":
			if info != nil {
				s.recordToolCall(ctx, req, info, res, err, t)
			}
		}
		return res, err
	}
}

func (s *Server) recordToolCall(ctx context.Context, req mcp.Request, info *callInfo, res mcp.Result, err error, t obs.Timer) {
	outcome := "ok"
	tokens := 0
	switch {
	case err != nil:
		outcome = "error"
	default:
		if r, ok := res.(*mcp.CallToolResult); ok {
			switch {
			case r.InputRequests != nil:
				outcome = "input_required"
			case r.IsError:
				outcome = "tool_error"
			}
			tokens = chunk.EstimateTokens(resultTextOf(r))
		}
	}
	s.m.toolCalls.With(info.tool, outcome).Inc()
	s.m.toolSeconds.With(info.tool).Observe(t.Seconds())
	s.m.toolTokens.With(info.tool).Observe(float64(tokens))
	client := sessionClient(req)
	attrs := []any{"tool", info.tool, "client", client, "latency_ms", fmt.Sprintf("%.1f", t.Elapsed().Seconds()*1000), "outcome", outcome, "n_results", info.nResults, "tokens_out", tokens}
	if info.class != "" {
		attrs = append(attrs, "error_class", info.class)
	}
	if info.err != nil {
		attrs = append(attrs, "err", info.err.Error())
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
	}
	s.logger.Info("tool call", attrs...)
	if s.callLog {
		s.persistCall(ctx, req, info, client, outcome, tokens, t)
	}
}

// observeTool wraps a typed handler so the Go error and the result size are
// visible to the middleware (the SDK folds errors into IsError).
func observeTool[In, Out any](s *Server, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if info := infoFrom(ctx); info != nil {
			if err != nil {
				info.err, info.class = err, classify(err)
				s.m.toolErrors.With(info.tool, info.class).Inc()
			} else {
				info.nResults = sizeOf(out)
			}
		}
		return res, out, err
	}
}

// classify buckets a tool error for metrics and the call log.
func classify(err error) string {
	var forgotten *kb.Forgotten
	var needs *kb.ErrNeedsHuman
	switch {
	case errors.Is(err, kb.ErrNotFound):
		return "not_found"
	case errors.As(err, &forgotten):
		return "forgotten"
	case errors.As(err, &needs):
		return "needs_human"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	}
	msg := err.Error()
	for _, hint := range []string{"is required", "must be one of", "must name", "usage:", "needs ", "bad ", "malformed", "not a memo://"} {
		if strings.Contains(msg, hint) {
			return "invalid_args"
		}
	}
	return "internal"
}

// sizeOf counts the results in a structured output.
func sizeOf(out any) int {
	switch v := out.(type) {
	case SearchOut:
		return len(v.Results)
	case ExploreOut:
		return len(v.Neighbours)
	case CompactOut:
		return len(v.Items)
	case ReadOut, IngestOut, RememberOut, ForgetOut, PromoteOut, StatusOut, SubmitOut:
		return 1
	}
	return 0
}

func resourceKind(uri string) string {
	switch {
	case uri == "memo://index":
		return "index"
	case strings.HasPrefix(uri, "memo://ns/"):
		return "ns_index"
	}
	kind, _, err := kb.ParseURI(uri)
	if err != nil {
		return "unknown"
	}
	return kind
}

// summarizeArgs keeps only the arguments that describe a call, never the
// content of what was written: safe to persist and to log.
func summarizeArgs(tool string, raw json.RawMessage) json.RawMessage {
	allow := map[string][]string{
		"search":   {"query", "queries", "mode", "scope", "granularity", "response_format", "max_tokens", "as_of", "exclude_ids"},
		"ingest":   {"namespace", "source"},
		"read":     {"uri", "granularity", "max_tokens"},
		"remember": {"namespace", "about", "evidence_uri", "supersedes", "valid_from", "valid_to"},
		"forget":   {"uri", "redact"},
		"promote":  {"uri", "to"},
		"explore":  {"entity", "namespace", "hops", "as_of"},
		"compact":  {"namespace", "kinds", "limit", "lint"},
		"submit":   {"item_id", "keep", "accept", "skip", "dry_run"},
		"status":   {},
	}[tool]
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return json.RawMessage(`{}`)
	}
	out := map[string]json.RawMessage{}
	for _, k := range allow {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if tool == "ingest" {
		if c, ok := in["content"]; ok {
			out["content_len"] = json.RawMessage(fmt.Sprint(len(c)))
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// persistCall writes the per-call row (Phase 3 wires the table).
func (s *Server) persistCall(ctx context.Context, req mcp.Request, info *callInfo, client, outcome string, tokens int, t obs.Timer) {
	var raw json.RawMessage
	if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
		raw = p.Arguments
	}
	entry := kb.CallLogEntry{At: time.Now(), Client: client, Method: "tools/call", Tool: info.tool, LatencyMs: t.Elapsed().Milliseconds(), OK: outcome == "ok" || outcome == "input_required", ErrorClass: info.class, NResults: info.nResults, TokensOut: tokens, Args: summarizeArgs(info.tool, raw)}
	if outcome == "error" && entry.ErrorClass == "" {
		entry.ErrorClass = "internal"
	}
	if err := s.store.LogCall(ctx, entry); err != nil {
		s.logger.Warn("call log write failed", "err", err)
	}
}

func sessionClient(req mcp.Request) string {
	name, _ := clientOf(req)
	return name
}

// clientOf is the calling client's name and version: from the request
// metadata under 2026-07-28, else from the session's initialize. The name
// is "unknown" when the client did not say.
func clientOf(req mcp.Request) (name, version string) {
	var impl *mcp.Implementation
	if r, ok := req.(interface{ ClientInfo() *mcp.Implementation }); ok {
		impl = r.ClientInfo()
	}
	if impl == nil || impl.Name == "" {
		return "unknown", ""
	}
	return impl.Name, impl.Version
}

// established reports whether a session outlives this request: the SDK
// closes a session whose initialisation (initialize, or discover on a
// transport that keeps 2026-07-28 sessions) did not complete.
func established(req mcp.Request) bool {
	ss, ok := req.GetSession().(*mcp.ServerSession)
	return ok && ss != nil && ss.InitializeParams() != nil
}
