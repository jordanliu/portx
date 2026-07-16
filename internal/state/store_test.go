package state

import (
	"testing"
)

func TestDataClonesProfilesMap(t *testing.T) {
	s, err := Open()
	if err != nil {
		// Open uses real config paths; use Replace on a fresh store via temp if needed.
		// Prefer in-memory construction:
		s = &Store{data: Data{Version: 1, Profiles: map[string]ProfileState{
			"personal": {TunnelID: "t1"},
		}}}
	}
	_ = s
	s = &Store{data: Data{
		Version: 1,
		Profiles: map[string]ProfileState{
			"personal": {TunnelID: "t1"},
		},
	}}

	snap := s.Data()
	snap.Profiles["personal"] = ProfileState{TunnelID: "mutated"}
	snap.Profiles["other"] = ProfileState{TunnelID: "x"}

	again := s.Data()
	if again.Profiles["personal"].TunnelID != "t1" {
		t.Fatalf("internal map mutated: %+v", again.Profiles["personal"])
	}
	if _, ok := again.Profiles["other"]; ok {
		t.Fatal("caller should not insert into store map")
	}
}
