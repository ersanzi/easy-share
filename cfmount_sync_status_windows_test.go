//go:build windows

package main

import (
	"testing"

	"easyshare/internal/cfapi"
	"easyshare/internal/cfmount"
	"easyshare/internal/shellstatus"
)

func TestNativeStatusPhaseUsesTrustedState(t *testing.T) {
	local := cfapi.PlaceholderStatus{Placeholder: true, FullContent: true, InSyncState: 1, RemoteID: 71, RemoteVersion: 3, Size: 9}
	baseline := cfmount.SyncBaseline{FileID: 71, Version: 3, Size: 9, LocalVerified: true}
	for _, test := range []struct {
		name     string
		local    cfapi.PlaceholderStatus
		baseline cfmount.SyncBaseline
		activity cfmount.WritebackStatus
		want     shellstatus.Phase
	}{
		{"trusted-local", local, baseline, cfmount.WritebackStatus{}, shellstatus.PhaseSynced},
		{"verification-not-completion", local, baseline, cfmount.WritebackStatus{Phase: cfmount.WritebackVerifying, Total: 9, Completed: 9}, shellstatus.PhaseVerifying},
		{"failure-wins", local, baseline, cfmount.WritebackStatus{Phase: cfmount.WritebackFailed}, shellstatus.PhaseFailed},
		{"conflict-wins", local, baseline, cfmount.WritebackStatus{Phase: cfmount.WritebackConflict}, shellstatus.PhaseConflict},
		{"new-remote-version", cfapi.PlaceholderStatus{Placeholder: true, InSyncState: 1, FullContent: true, RemoteID: 71, RemoteVersion: 4, Size: 9}, baseline, cfmount.WritebackStatus{}, shellstatus.PhaseConflict},
		{"ordinary-unknown", cfapi.PlaceholderStatus{}, cfmount.SyncBaseline{}, cfmount.WritebackStatus{}, shellstatus.PhaseUnknown},
		{"partial-cached", cfapi.PlaceholderStatus{Placeholder: true, InSyncState: 1, OnDisk: 3, Size: 9}, cfmount.SyncBaseline{}, cfmount.WritebackStatus{}, shellstatus.PhaseUnknown},
		{"online-only", cfapi.PlaceholderStatus{Placeholder: true, InSyncState: 1, OnDisk: 0, Size: 9}, cfmount.SyncBaseline{}, cfmount.WritebackStatus{}, shellstatus.PhaseSynced},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeStatusPhase(test.local, test.baseline, test.activity); got != test.want {
				t.Fatalf("phase=%s want=%s", got, test.want)
			}
		})
	}
}
