package server

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
)

var dayRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// handlePutUsage records the caller's counts. Re-sending a day replaces it,
// so clients can upload their full recent history idempotently. Counts are
// only accepted for packages published in the team.
func (s *Server) handlePutUsage(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r, "member")
	if !ok {
		return
	}
	var req []api.UsageCount
	if !readJSON(w, r, &req) {
		return
	}
	if len(req) > 5000 {
		writeErr(w, http.StatusBadRequest, "too many entries")
		return
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer tx.Rollback()
	accepted := 0
	for _, u := range req {
		if !dayRE.MatchString(u.Day) || u.Count < 0 || u.Count > 1e6 {
			continue
		}
		var x int
		if tx.QueryRow(`SELECT 1 FROM packages WHERE team_id=? AND type=? AND name=?`, t.ID, u.Type, u.Name).Scan(&x) != nil {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO usage_daily(team_id,type,name,day,user_id,count) VALUES(?,?,?,?,?,?)
			ON CONFLICT(team_id,type,name,day,user_id) DO UPDATE SET count=excluded.count`, t.ID, u.Type, u.Name, u.Day, userOf(r), u.Count); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		accepted++
	}
	if err := tx.Commit(); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": accepted})
}

func (s *Server) handleGetUsage(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r, "member")
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	since := s.cfg.Now().AddDate(0, 0, -days).UTC().Format(time.DateOnly)
	rows, err := s.st.rdb.Query(`SELECT type, name, CAST(SUM(count) AS BIGINT), COUNT(DISTINCT user_id) FROM usage_daily
		WHERE team_id=? AND day >= ? GROUP BY type, name ORDER BY SUM(count) DESC, name`, t.ID, since)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer rows.Close()
	out := []api.UsageRank{}
	for rows.Next() {
		var u api.UsageRank
		if err := rows.Scan(&u.Type, &u.Name, &u.Count, &u.Users); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

var skillNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// handlePutUserUsage records the caller's own skill usage across their
// machines. Re-sending a day replaces it (idempotent).
func (s *Server) handlePutUserUsage(w http.ResponseWriter, r *http.Request) {
	var req []api.UsageCount
	if !readJSON(w, r, &req) {
		return
	}
	if len(req) > 5000 {
		writeErr(w, http.StatusBadRequest, "too many entries")
		return
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer tx.Rollback()
	accepted := 0
	for _, u := range req {
		if (u.Type != "skill" && u.Type != "agent") || !dayRE.MatchString(u.Day) || !skillNameRE.MatchString(u.Name) || u.Count < 0 || u.Count > 1e6 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO user_usage_daily(user_id,type,name,day,count) VALUES(?,?,?,?,?)
			ON CONFLICT(user_id,type,name,day) DO UPDATE SET count=excluded.count`, userOf(r), u.Type, u.Name, u.Day, u.Count); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		accepted++
	}
	if err := tx.Commit(); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": accepted})
}

// handlePruneUserUsage deletes the caller's counts of the named skills.
func (s *Server) handlePruneUserUsage(w http.ResponseWriter, r *http.Request) {
	var req api.UsagePruneRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Names) == 0 || len(req.Names) > 1000 {
		writeErr(w, http.StatusBadRequest, "give 1..1000 skill names")
		return
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer tx.Rollback()
	var deleted int64
	for _, n := range req.Names {
		res, err := tx.Exec(`DELETE FROM user_usage_daily WHERE user_id=? AND name=?`, userOf(r), n)
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		k, _ := res.RowsAffected()
		deleted += k
	}
	if err := tx.Commit(); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

// handleGetUserUsage ranks the caller's skills (or ?type=agent agents)
// across all their machines.
func (s *Server) handleGetUserUsage(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	if typ != "agent" {
		typ = "skill"
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	since := s.cfg.Now().AddDate(0, 0, -days).UTC().Format(time.DateOnly)
	rows, err := s.st.rdb.Query(`SELECT type, name, CAST(SUM(count) AS BIGINT), COUNT(DISTINCT day)
		FROM user_usage_daily WHERE user_id=? AND type=? AND day>=?
		GROUP BY type, name ORDER BY SUM(count) DESC, name LIMIT ?`, userOf(r), typ, since, limit)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer rows.Close()
	out := []api.UsageRank{}
	for rows.Next() {
		var u api.UsageRank
		if err := rows.Scan(&u.Type, &u.Name, &u.Count, &u.Users); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}
