package client

import (
	"path/filepath"
)

// shareState is this machine's memory of what it last published to a
// team. Auto-sharing may replace only that copy: another machine or member
// may have a different version in the team, and that must not be
// overwritten every 20 minutes (the two copies would ping-pong forever).
type shareState struct {
	// Published maps "team/type/name" to the SHA-256 this machine last
	// published, or adopted as the team's current content.
	Published map[string]string `json:"published"`
}

func shareStatePath(home string) string { return filepath.Join(home, "share-state.json") }

func loadShareState(home string) shareState {
	st := shareState{Published: map[string]string{}}
	_ = loadJSON(shareStatePath(home), &st)
	if st.Published == nil {
		st.Published = map[string]string{}
	}
	return st
}

func shareKey(team, typ, name string) string { return team + "/" + typ + "/" + name }

func (st *shareState) set(team, typ, name, sum string) bool {
	key := shareKey(team, typ, name)
	if st.Published[key] == sum {
		return false
	}
	st.Published[key] = sum
	return true
}

func (st *shareState) get(team, typ, name string) string {
	return st.Published[shareKey(team, typ, name)]
}

func (st *shareState) save(home string) error {
	return saveJSON(shareStatePath(home), st)
}
