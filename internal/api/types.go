// Package api defines the JSON wire types shared by client and server.
// []byte fields are base64-encoded by encoding/json.
package api

import (
	"strconv"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// MaxChunkBytes bounds one uploaded ciphertext chunk.
const MaxChunkBytes = 8 << 20

// Header names for chunk uploads.
const (
	HeaderPlainLength   = "X-Hearth-Plain-Length"
	HeaderGeneration    = "X-Hearth-Generation"
	HeaderSize          = "X-Hearth-Size"
	HeaderClientVersion = "X-Hearth-Version" // client build, shown in the admin panel
	HeaderTotalCount    = "X-Hearth-Total-Count"

	HeaderLibraryVersion = "X-Hearth-Library-Version"
	HeaderLibrarySHA256  = "X-Hearth-Library-Sha256"
	HeaderDescription    = "X-Hearth-Description" // URL-encoded
)

// MaxLibraryBytes bounds one compressed library bundle.
const MaxLibraryBytes = 32 << 20

type PreloginRequest struct {
	Email string `json:"email"`
}

type PreloginResponse struct {
	KDF  crypto.KDFParams `json:"kdf"`
	Salt []byte           `json:"salt"`
}

type RegisterRequest struct {
	Email               string           `json:"email"`
	KDF                 crypto.KDFParams `json:"kdf"`
	Salt                []byte           `json:"salt"`
	AuthKey             []byte           `json:"auth_key"`
	EncVaultKey         []byte           `json:"enc_vault_key"`
	EncVaultKeyRecovery []byte           `json:"enc_vault_key_recovery"`
	RecoveryAuth        []byte           `json:"recovery_auth"` // HKDF(recovery key), proves possession later
	InviteCode          string           `json:"invite_code,omitempty"`
}

// NewCredentials replaces the password; the vault key itself is unchanged.
type NewCredentials struct {
	KDF         crypto.KDFParams `json:"kdf"`
	Salt        []byte           `json:"salt"`
	AuthKey     []byte           `json:"auth_key"`
	EncVaultKey []byte           `json:"enc_vault_key"`
}

type ChangePasswordRequest struct {
	OldAuthKey []byte         `json:"old_auth_key"`
	New        NewCredentials `json:"new"`
}

// RotateRecoveryRequest replaces the recovery key (the old one leaked or was
// lost). The password proves it is the account owner; the vault key and
// every device keep working.
type RotateRecoveryRequest struct {
	AuthKey             []byte `json:"auth_key"`
	EncVaultKeyRecovery []byte `json:"enc_vault_key_recovery"`
	RecoveryAuth        []byte `json:"recovery_auth"`
}

type RecoveryRequest struct {
	Email        string          `json:"email"`
	RecoveryAuth []byte          `json:"recovery_auth"`
	New          *NewCredentials `json:"new,omitempty"` // nil: only fetch the wrapped vault key
}

type RecoveryResponse struct {
	EncVaultKeyRecovery []byte `json:"enc_vault_key_recovery"`
}

type LoginRequest struct {
	Email      string `json:"email"`
	AuthKey    []byte `json:"auth_key"`
	DeviceName string `json:"device_name"`
}

type LoginResponse struct {
	Token       string `json:"token"`
	DeviceID    string `json:"device_id"`
	ExpiresAt   int64  `json:"expires_at"`
	EncVaultKey []byte `json:"enc_vault_key"`
}

// Platform identity login: a server-side broker (the platform's delegated
// trust service) resolves the account's platform secret, and the client
// proves possession of the same secret without sending it.
type IdentityEnableRequest struct {
	EncVaultKeyIdentity []byte `json:"enc_vault_key_identity"`
	Force               bool   `json:"force,omitempty"` // undo an explicit disable
}

type IdentityLoginRequest struct {
	Email      string `json:"email"`
	Timestamp  int64  `json:"timestamp"`
	Proof      []byte `json:"proof"`
	DeviceName string `json:"device_name"`
}

type IdentityLoginResponse struct {
	Token               string `json:"token"`
	DeviceID            string `json:"device_id"`
	ExpiresAt           int64  `json:"expires_at"`
	EncVaultKeyIdentity []byte `json:"enc_vault_key_identity"`
}

type IdentityStatusResponse struct {
	Enabled  bool `json:"enabled"`
	Disabled bool `json:"disabled"` // the user opted out; automatic enrollment stops
}

// IdentityLoginMessage is the message an identity proof signs. Client and
// server must build it identically.
func IdentityLoginMessage(email string, ts int64) string {
	return "hearth-identity-login:" + strings.ToLower(strings.TrimSpace(email)) + ":" + strconv.FormatInt(ts, 10)
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	LastSeen int64  `json:"last_seen"`
	// Online: the device talked to the server in the last few minutes (its
	// daemon heartbeats every minute) and did not sign off since.
	Online bool `json:"online"`
}

type PutSessionRequest struct {
	EncMeta []byte `json:"enc_meta"`
}

type Session struct {
	ID             string `json:"id"`
	EncMeta        []byte `json:"enc_meta"`
	Generation     int64  `json:"generation"`
	Size           int64  `json:"size"`
	Status         string `json:"status"`
	Pinned         bool   `json:"pinned"`
	LastAccessedAt int64  `json:"last_accessed_at"`
	ExpiredAt      *int64 `json:"expired_at,omitempty"`
	LeaseDevice    string `json:"lease_device,omitempty"`
	LeaseExpiresAt int64  `json:"lease_expires_at,omitempty"`
	// ActiveAt is when content was last appended (newest first in lists).
	ActiveAt int64 `json:"active_at,omitempty"`
	// UpdatedBy is the device id that last wrote the session.
	UpdatedBy string `json:"updated_by,omitempty"`
}

type Chunk struct {
	Generation int64 `json:"generation"`
	Offset     int64 `json:"offset"`
	Length     int64 `json:"length"`
}

type LeaseRequest struct {
	TTLSeconds int64 `json:"ttl_seconds"`
	Force      bool  `json:"force"`
}

type PinRequest struct {
	Pinned bool `json:"pinned"`
}

// Error is the body of every non-2xx response. Generation/Size are set on
// 409 conflicts so the client can resynchronise.
type Error struct {
	Error      string `json:"error"`
	Generation int64  `json:"generation,omitempty"`
	Size       int64  `json:"size,omitempty"`
	LeaseOwner string `json:"lease_owner,omitempty"`
}

type CreateTeamRequest struct {
	Name string `json:"name"`
}

type Team struct {
	Name string `json:"name"`
	// Role: owner | maintainer (can publish) | member, or reader: not a
	// member, but one of the user's teams was given read access.
	Role string `json:"role"`
	// Via: for readers, the user's team the packages are shared with.
	Via string `json:"via,omitempty"`
}

// CreateInviteRequest makes a code that lets anyone with an account join.
type CreateInviteRequest struct {
	Role     string `json:"role"`      // member | maintainer (default) | owner (owners only)
	TTLHours int    `json:"ttl_hours"` // default 168 (a week), max 8760
	MaxUses  int    `json:"max_uses"`  // 0 = unlimited until it expires
}

type Invite struct {
	Code      string `json:"code"`
	Team      string `json:"team"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"expires_at"`
	MaxUses   int    `json:"max_uses,omitempty"`
}

type JoinTeamRequest struct {
	Code string `json:"code"`
}

// TeamShares: which teams can read this team's packages, and which teams
// share theirs with this one.
type TeamShares struct {
	SharedWith []string `json:"shared_with"`
	SharedFrom []string `json:"shared_from"`
}

type SetMemberRequest struct {
	Role string `json:"role"`
}

type Member struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Package is the latest version of a shared skill or agent.
type Package struct {
	Type        string `json:"type"` // skill | agent
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int64  `json:"version"`
	SHA256      string `json:"sha256"`
	PublishedBy string `json:"published_by"`
	PublishedAt int64  `json:"published_at"`
	// Heat: uses by the team's members in the last 30 days, and by how
	// many of them (servers before 0.1.0-20261027 leave these 0). The list
	// is sorted hottest first.
	Uses  int64 `json:"uses,omitempty"`
	Users int64 `json:"users,omitempty"`
}

type PublishResponse struct {
	Version int64 `json:"version"`
	Created bool  `json:"created"` // false if identical to the latest version
}

// Secret is an end-to-end encrypted credential.
type Secret struct {
	ID         string `json:"id"`
	Ciphertext []byte `json:"ciphertext"`
	UpdatedAt  int64  `json:"updated_at"`
}

type PutSecretRequest struct {
	Ciphertext []byte `json:"ciphertext"`
}

// Limits for secrets.
const (
	MaxSecretBytes    = 64 << 10
	MaxSecretsPerUser = 500
)

// UsageCount is one user's count for a team package on one day (YYYY-MM-DD).
type UsageCount struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// UsagePruneRequest removes the caller's usage counts of these skills
// (deleted, renamed or test skills), every day.
type UsagePruneRequest struct {
	Names []string `json:"names"`
}

// UsageRank is a package's total over a period.
type UsageRank struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Users int64  `json:"users"`
}

// SearchHit is one result of a team package search.
type SearchHit struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Version     int64   `json:"version"`
	Score       float64 `json:"score"`
}

// AccountUsage is GET /v1/account/usage.
type AccountUsage struct {
	Used         int64 `json:"used"`
	Quota        int64 `json:"quota"` // 0 = unlimited
	Sessions     int64 `json:"sessions"`
	SessionBytes int64 `json:"session_bytes"`
	LibraryBytes int64 `json:"library_bytes"`
	SecretBytes  int64 `json:"secret_bytes"`
	Devices      int64 `json:"devices"`
}
