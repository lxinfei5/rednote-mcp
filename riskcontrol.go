package main

// XHS risk-control cooldown — the SERVER-SIDE circuit breaker.
//
// The playbooks (xiaohongshu_search_playbook / source_health_and_degradation) mandate STOP-on-
// risk-signal: on ANY 扫码 / 验证码 / 操作频繁 / empty-after-success detected in the search RESULT,
// the source must cool down — never retry-until-it-works (狂刷 = escalating a soft limit into an
// irreversible hard ban).
//
// CRITICAL: browser INFRASTRUCTURE errors (Chrome launch failure, Rod session loss, page open
// failure) do NOT trigger cooldown. These are local transient faults, NOT XHS risk-control signals.
// Treating them as risk signals causes a single transient Chrome profile conflict to block ALL
// search for the cooldown duration — a disastrous false positive.
//
// This is the ONLY layer that protects workflow sub-agents that call mcp__xiaohongshu-mcp__*
// DIRECTLY — they bypass the ROS bridge (ros/lib/social_pacing.py), which only covers `ros xhs`.
// File-backed under the binary's CWD (./cooldown.json) so the cooldown is shared across the MCP
// server, `ros xhs`, and concurrent sub-agents — a signal seen by any one of them stops all of them.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const xhsCooldownDuration = 5 * time.Minute

// xhsRiskMarkers — pure substring match against the JSON-marshaled RESULT (not error strings).
// Browser-infra errors do NOT appear here; they are handled separately and do NOT trip cooldown.
var xhsRiskMarkers = []string{
	"扫码", "请打开 App", "请打开App", "验证码", "滑块", "操作频繁", "操作太频繁", "稍后再试",
	"risk_control", "blocked", "unauthorized",
}

// xhsBrowserInfraErrors — substring matches against browser/infrastructure errors that should
// NEVER trigger cooldown. These are local faults (Chrome profile conflict, Rod session loss,
// page open failure), not XHS risk-control signals.
var xhsBrowserInfraErrors = []string{
	"打开页面失败",
	"Failed to get the debug url",
	"正在现有的浏览器会话中打开",
	"Session with given id not found",
	"browser 健康检查失败",
	"context deadline exceeded",
	"no such window",
	"target closed",
}

type cooldownRecord struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
	SetAt  time.Time `json:"set_at"`
}

var cooldownMu sync.Mutex

func cooldownFile() string {
	// rednote-mcp's own state, independent of ResearchOS. Uses CWD (binary working directory)
	// — same convention as cookies.json (resolved relatively). The daemon script sets cwd to
	// the canonical dir; manual runs inherit the user's cwd.
	return "cooldown.json"
}

// xhsCooldownActive returns the active record if XHS is currently in cooldown (and lazily clears
// an expired one).
func xhsCooldownActive() (cooldownRecord, bool) {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	b, err := os.ReadFile(cooldownFile())
	if err != nil {
		return cooldownRecord{}, false
	}
	var rec cooldownRecord
	if json.Unmarshal(b, &rec) != nil {
		return cooldownRecord{}, false
	}
	if time.Now().After(rec.Until) {
		_ = os.Remove(cooldownFile())
		return cooldownRecord{}, false
	}
	return rec, true
}

func xhsSetCooldown(reason string) cooldownRecord {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	rec := cooldownRecord{Until: time.Now().Add(xhsCooldownDuration), Reason: reason, SetAt: time.Now()}
	if b, err := json.Marshal(rec); err == nil {
		_ = os.WriteFile(cooldownFile(), b, 0o600)
	}
	logrus.Warnf("XHS cooldown set for %v: %s — risk-control signal detected; further dispatch refused",
		xhsCooldownDuration, reason)
	return rec
}

// xhsClearCooldown removes the cooldown file. Useful for manual reset or testing.
func xhsClearCooldown() {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	_ = os.Remove(cooldownFile())
	logrus.Info("XHS cooldown cleared (manual reset)")
}

// xhsGateCheck returns an error (surfaced as the MCP result so callers/agents see the STOP signal)
// when XHS is in cooldown. op labels the operation for the message.
func xhsGateCheck(op string) error {
	if rec, active := xhsCooldownActive(); active {
		return fmt.Errorf("XHS %s refused: source in COOLDOWN until %s (%s). A risk-control signal was "+
			"seen on an earlier call; retrying now risks escalating a soft limit into an irreversible "+
			"account ban. Wait for the cooldown to expire (or clear %s).",
			op, rec.Until.Format(time.RFC3339), rec.Reason, cooldownFile())
	}
	return nil
}

// isBrowserInfraError checks whether an error string matches known browser-infrastructure
// failure patterns (Chrome profile conflict, Rod session loss, etc.) — these are local
// transient faults that should NOT be treated as XHS risk-control signals.
func isBrowserInfraError(errStr string) bool {
	for _, p := range xhsBrowserInfraErrors {
		if strings.Contains(errStr, p) {
			return true
		}
	}
	return false
}

// xhsMarkRisk inspects a search/detail RESULT (not an error) for risk-control signals and sets
// cooldown. Only the RESULT blob is inspected — browser-infra errors are handled by the caller
// and should NOT be passed here (they are local faults, not XHS risk signals).
//
// NOTE: "EOF" was intentionally removed from the marker list. EOF is a generic network error
// that Rod often surfaces on timeout/connection-reset — it is NOT a specific XHS risk signal.
// If XHS truly blocks, the result blob will contain "扫码" / "验证码" / "操作频繁" instead.
func xhsMarkRisk(op string, blob string) {
	for _, m := range xhsRiskMarkers {
		if strings.Contains(blob, m) {
			xhsSetCooldown(fmt.Sprintf("%s risk marker %q", op, m))
			return
		}
	}
}
