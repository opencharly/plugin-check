// session_seam.go — the RUNNER-OWNED background-session service's provider-facing seam
// (Cutover A, A-task-2): the compiled-in `verb:session` capability capture providers reach
// over the EXISTING RunHostStep reverse leg (charly's plugin_executor_reverse.go — the same
// host-step leg every out-of-process deploy plugin drives; in-proc placement rides the
// in-proc executor client, so the provider code is placement-invisible).
//
// A session provider (plugin-spice's session start, Cutover A-task-2b) submits a GENERIC
// host-step request — its recorder command + session identity, NEVER a systemd unit — and
// the runner (this plugin, compiled-in) dispatches it here. The provider never learns the
// transport, spawns no process, owns no pidfile.
//
// Wire shape (the contract plugin-spice/plugin-record consume; documented in
// /charly-internals:plugin authoring recipes):
//
//	{ "op": "spawn"|"stop"|"status"|"sweep",
//	  "session_id": "<venue-scoped id>",
//	  "command":    ["<recorder argv>"],          // spawn only
//	  "dir":        "<recorder working dir>",     // spawn only
//	  "env":        {"K": "V", ...},              // spawn only
//	  "log_dir":    "<.check/<bed>/<calver>>" }   // the run dir the capture/ state lives in
//
// Success is the empty reply (invokeExternalStep returns it as a successful host-step);
// failure travels in the HostStepReply.Error (the runReply convention). The service writes
// the handle into the state dir, so a later stop/status call needs only the session id;
// nothing session-shaped rides the reply.

package check

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/opencharly/spec/proto"
)

// sessionSeamOp is the discriminator for a session-service request arriving over the
// reverse leg. The vocabulary is the runner's generic lifecycle: spawn (start a recorder
// detached), stop (signal finalize), status (liveness), sweep (reap stale handles).
type sessionSeamOp string

const (
	sessionSeamSpawn  sessionSeamOp = "spawn"
	sessionSeamStop   sessionSeamOp = "stop"
	sessionSeamStatus sessionSeamOp = "status"
	sessionSeamSweep  sessionSeamOp = "sweep"
)

// sessionSeamRequest is the wire decode of a verb:session request (see the header).
type sessionSeamRequest struct {
	Op        sessionSeamOp     `json:"op"`
	SessionID string            `json:"session_id"`
	Command   []string          `json:"command,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	LogDir    string            `json:"log_dir,omitempty"`
}

// sessionSeamReply is the wire reply body. On success it is empty (the host-step call
// itself succeeded); status carries the liveness answer.
type sessionSeamReply struct {
	Alive bool `json:"alive,omitempty"`
}

// dispatchSessionSeam serves a verb:session request arriving through the host's
// RunHostStep ExternalPluginStep arm (invokeExternalStep → InvokeProvider with
// Class="verb", Reserved="session", Op=OpExecute). Errors return as the provider error,
// which the host folds into the HostStepReply.Error the caller's RunHostStep surfaces.
func dispatchSessionSeam(ctx context.Context, params []byte) (*pb.InvokeReply, error) {
	var req sessionSeamRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("verb:session: decode request: %w", err)
		}
	}
	if req.SessionID == "" && req.Op != sessionSeamSweep {
		return nil, fmt.Errorf("verb:session: empty session_id")
	}
	switch req.Op {
	case sessionSeamSpawn:
		if _, err := spawnSession(ctx, sessionSpawnOpts{
			SessionID: req.SessionID,
			Command:   req.Command,
			Dir:       req.Dir,
			Env:       req.Env,
			LogDir:    req.LogDir,
		}); err != nil {
			return nil, err
		}
		return &pb.InvokeReply{}, nil
	case sessionSeamStop:
		h, err := SessionHandleFromDisk(sessionStateDir(req.LogDir, req.SessionID))
		if err != nil {
			return nil, fmt.Errorf("verb:session stop %q: %w", req.SessionID, err)
		}
		if h == nil {
			return nil, fmt.Errorf("verb:session stop %q: no session on record", req.SessionID)
		}
		stopSession(ctx, h)
		return &pb.InvokeReply{}, nil
	case sessionSeamStatus:
		h, err := SessionHandleFromDisk(sessionStateDir(req.LogDir, req.SessionID))
		if err != nil || h == nil {
			return &pb.InvokeReply{ResultJson: mustJSON(sessionSeamReply{Alive: false})}, nil
		}
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return &pb.InvokeReply{ResultJson: mustJSON(sessionSeamReply{Alive: SessionLiveness(sctx, h)})}, nil
	case sessionSeamSweep:
		sweepStaleSessions(ctx, req.LogDir)
		return &pb.InvokeReply{}, nil
	default:
		return nil, fmt.Errorf("verb:session: unsupported op %q", req.Op)
	}
}

// mustJSON marshals a reply body; a marshal failure is a programming error, not a wire
// failure (the shapes are tiny and fixed).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
