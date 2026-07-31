package server

import (
	"testing"

	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestRegisteredMsgIDsIncludesUpstreamRanges(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{
		RouteMsgIDs: []uint32{1000},
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "http", MsgIDMin: 1000, MsgIDMax: 1002},
			{Name: "nsq", MsgIDMin: 2000, MsgIDMax: 2001},
		},
	})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{2, 1000, 1001, 1002, 2000, 2001}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for i := range want {
		if msgIDs[i] != want[i] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsAlwaysIncludesControlMessages(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{protocol.MsgIDDownlinkAck, protocol.MsgIDBind}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for i := range want {
		if msgIDs[i] != want[i] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsIncludesReloadAdmissionRanges(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "current", MsgIDMin: 1001, MsgIDMax: 1002},
		},
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Reload: UpstreamRouteReloadConfig{
				Enabled: true,
				AcceptedMsgIDRanges: []MsgIDRange{
					{Min: 1001, Max: 1003},
					{Min: 2001, Max: 2002},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{2, 1000, 1001, 1002, 1003, 2001, 2002}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for index := range want {
		if msgIDs[index] != want[index] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsRejectsReloadWithoutAdmissionRanges(t *testing.T) {
	_, err := registeredMsgIDs(Config{
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Reload: UpstreamRouteReloadConfig{Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("registeredMsgIDs() error = nil, want error")
	}
}

func TestRegisteredMsgIDsRejectsInvalidRange(t *testing.T) {
	_, err := registeredMsgIDs(Config{
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "broken", MsgIDMin: 2000, MsgIDMax: 1000},
		},
	})
	if err == nil {
		t.Fatal("registeredMsgIDs() error = nil, want error")
	}
}

func TestCompactMsgIDRanges(t *testing.T) {
	got := compactMsgIDRanges([]uint32{2, 1000, 1001, 1002, 2000, 2001, 3000})
	want := []string{"2", "1000-1002", "2000-2001", "3000"}
	if len(got) != len(want) {
		t.Fatalf("compactMsgIDRanges() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compactMsgIDRanges() = %v, want %v", got, want)
		}
	}
}
