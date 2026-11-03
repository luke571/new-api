package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Internal mode is opt-in and preserves legacy deployments by default.
func InternalModeEnabled() bool { return os.Getenv("INTERNAL_MODE_ENABLED") == "true" }
func InternalCodexModel(name string) bool {
	for _, allowed := range strings.Split(os.Getenv("INTERNAL_CODEX_MODELS"), ",") {
		if name == strings.TrimSpace(allowed) && name != "" {
			return true
		}
	}
	return name == "gpt-6.1-sol" || name == "gpt-6.1-terra" || name == "gpt-6-luna"
}
func InternalOpenAIModel(name string) bool {
	return strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "codex-")
}

// Tokens are returned once; only a hash is persisted.
type InternalInvite struct {
	ID        int    `json:"id"`
	Hash      string `json:"-" gorm:"type:varchar(64);uniqueIndex;not null"`
	Kind      string `json:"kind" gorm:"type:varchar(16)"`
	UserID    int    `json:"user_id"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at"`
	CreatedBy int    `json:"created_by"`
}
type InternalTeam struct {
	ID      int    `json:"id"`
	Name    string `json:"name" gorm:"type:varchar(100);not null"`
	OwnerID int    `json:"owner_id" gorm:"uniqueIndex;not null"`
}
type InternalMember struct {
	ID             int   `json:"id"`
	UserID         int   `json:"user_id" gorm:"uniqueIndex;not null"`
	TeamID         int   `json:"team_id" gorm:"index;not null"`
	QuotaLimit     int64 `json:"quota_limit"`
	QuotaUsed      int64 `json:"quota_used"`
	SubscriptionID int   `json:"subscription_id"`
	Active         bool  `json:"active"`
}
type InternalAddon struct {
	ID             int   `json:"id"`
	SubscriptionID int   `json:"subscription_id" gorm:"index;not null"`
	AmountTotal    int64 `json:"amount_total"`
	AmountUsed     int64 `json:"amount_used"`
	ExpiresAt      int64 `json:"expires_at"`
	CreatedBy      int   `json:"created_by"`
}
type internalAllocation struct {
	AddonID int   `json:"addon_id"`
	Amount  int64 `json:"amount"`
}
type InternalCharge struct {
	RequestID      string `json:"request_id" gorm:"primaryKey;type:varchar(100)"`
	UserID         int    `json:"user_id" gorm:"index"`
	OwnerID        int    `json:"owner_id" gorm:"index"`
	TeamID         int    `json:"team_id" gorm:"index"`
	MemberID       int    `json:"member_id"`
	SubscriptionID int    `json:"subscription_id" gorm:"index"`
	Period         int64  `json:"period"`
	Amount         int64  `json:"amount"`
	BaseAmount     int64  `json:"base_amount"`
	Allocations    string `json:"-" gorm:"type:text"`
	Status         string `json:"status" gorm:"type:varchar(16)"`
	CreatedAt      int64  `json:"created_at"`
}

func NewInternalInvite(actor, userID int) (*InternalInvite, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	kind, ttl := "register", int64(86400)
	if userID > 0 {
		kind, ttl = "reset", 900
		var user User
		if err := DB.First(&user, userID).Error; err != nil {
			return nil, "", err
		}
		if user.Role >= common.RoleAdminUser {
			return nil, "", errors.New("administrator recovery requires the existing secure recovery flow")
		}
	}
	row := &InternalInvite{Hash: hex.EncodeToString(hash[:]), Kind: kind, UserID: userID, ExpiresAt: common.GetTimestamp() + ttl, CreatedBy: actor}
	err := DB.Create(row).Error
	return row, token, err
}
func consumeInternalInvite(tx *gorm.DB, token, kind string) (*InternalInvite, error) {
	if len(token) != 64 {
		return nil, errors.New("invitation or recovery token is invalid or expired")
	}
	hash := sha256.Sum256([]byte(token))
	var row InternalInvite
	if err := lockForUpdate(tx).Where("hash = ? AND kind = ?", hex.EncodeToString(hash[:]), kind).First(&row).Error; err != nil {
		return nil, errors.New("invitation or recovery token is invalid or expired")
	}
	now := common.GetTimestamp()
	result := tx.Model(&InternalInvite{}).Where("id = ? AND used_at = 0 AND expires_at > ?", row.ID, now).Update("used_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("invitation or recovery token is invalid or expired")
	}
	return &row, nil
}
func RegisterInternalUser(user *User, token string) error {
	if utf8.RuneCountInString(user.Password) < 15 {
		return errors.New("password must contain at least 15 characters")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		invite, err := consumeInternalInvite(tx, token, "register")
		if err != nil {
			return err
		}
		if err = user.InsertWithTx(tx, 0); err != nil {
			return err
		}
		user.Quota = 0
		if err = tx.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error; err != nil {
			return err
		}
		return tx.Model(&InternalInvite{}).Where("id = ?", invite.ID).Update("user_id", user.Id).Error
	})
	if err == nil {
		user.FinishInsert(0)
	}
	return err
}
func ResetInternalPassword(token, password string) error {
	if utf8.RuneCountInString(password) < 15 || utf8.RuneCountInString(password) > 128 {
		return errors.New("password must contain 15 to 128 characters")
	}
	hash, err := common.HashAccountPassword(password)
	if err != nil {
		return err
	}
	userID := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		invite, err := consumeInternalInvite(tx, token, "reset")
		if err != nil {
			return err
		}
		var user User
		if err = lockForUpdate(tx).First(&user, invite.UserID).Error; err != nil {
			return err
		}
		if user.Role >= common.RoleAdminUser {
			return errors.New("administrator recovery requires the existing secure recovery flow")
		}
		if _, err = IncrementUserAuthVersionWithTx(tx, user.Id); err != nil {
			return err
		}
		userID = user.Id
		return tx.Model(&User{}).Where("id = ?", user.Id).Update("password", hash).Error
	})
	if err != nil {
		return err
	}
	if err = PublishUserAuthCache(userID); err != nil {
		return err
	}
	_, err = RevokeAllUserSessions(userID, "internal_password_reset")
	return err
}

// User-row locks serialize membership and subscription ownership changes.
func internalLockUser(tx *gorm.DB, id int) error {
	var user User
	if err := lockForUpdate(tx).First(&user, id).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return errors.New("account is disabled")
	}
	return nil
}
func InternalOwner(tx *gorm.DB, userID int) (int, *InternalMember, error) {
	tx = tx.Session(&gorm.Session{})
	var member InternalMember
	err := tx.Where("user_id = ?", userID).First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return userID, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if !member.Active {
		return userID, nil, nil
	}
	var team InternalTeam
	if err = tx.First(&team, member.TeamID).Error; err != nil {
		return 0, nil, err
	}
	return team.OwnerID, &member, nil
}
func CreateInternalTeam(name string, ownerID int) (*InternalTeam, error) {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 100 {
		return nil, errors.New("invalid team name")
	}
	team := &InternalTeam{Name: name, OwnerID: ownerID}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := internalLockUser(tx, ownerID); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&InternalMember{}).Where("user_id = ?", ownerID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("owner already has a team membership")
		}
		if err := tx.Create(team).Error; err != nil {
			return err
		}
		return tx.Create(&InternalMember{UserID: ownerID, TeamID: team.ID, Active: true}).Error
	})
	return team, err
}
func SetInternalMember(actor, teamID, userID int, limit int64, active bool, root bool) error {
	if limit < 0 || limit > int64(common.MaxWalletQuota) {
		return errors.New("invalid member quota limit")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var team InternalTeam
		if err := tx.First(&team, teamID).Error; err != nil {
			return err
		}
		if err := internalLockUser(tx, team.OwnerID); err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&team, teamID).Error; err != nil {
			return err
		}
		if !root && team.OwnerID != actor {
			return errors.New("team owner permission required")
		}
		if userID == team.OwnerID && !active {
			return errors.New("team owner cannot be removed")
		}
		if err := internalLockUser(tx, userID); err != nil {
			return err
		}
		var personal int64
		if userID != team.OwnerID {
			if err := tx.Model(&UserSubscription{}).Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", common.GetTimestamp()).Count(&personal).Error; err != nil {
				return err
			}
			if personal > 0 {
				return errors.New("expire the personal subscription before joining a team")
			}
		}
		var member InternalMember
		err := tx.Where("user_id = ?", userID).First(&member).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&InternalMember{UserID: userID, TeamID: team.ID, QuotaLimit: limit, Active: active}).Error
		}
		if err != nil {
			return err
		}
		if member.TeamID != team.ID {
			if member.Active || !root {
				return errors.New("user already belongs to another team")
			}
			member.TeamID = team.ID
			member.SubscriptionID = 0
			member.QuotaUsed = 0
			member.Active = active
			member.QuotaLimit = limit
			return tx.Save(&member).Error
		}
		return tx.Model(&member).Updates(map[string]any{"active": active, "quota_limit": limit}).Error
	})
}
func GrantInternalAddon(subscriptionID int, amount int64, actor int) (*InternalAddon, error) {
	if amount != 5000000 && amount != 15000000 && amount != 30000000 {
		return nil, errors.New("invalid add-on tier")
	}
	addon := &InternalAddon{SubscriptionID: subscriptionID, AmountTotal: amount, CreatedBy: actor}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).First(&sub, subscriptionID).Error; err != nil {
			return err
		}
		if sub.Status != "active" || sub.EndTime <= GetDBTimestamp(tx) {
			return errors.New("active monthly subscription required")
		}
		plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
		if err != nil {
			return err
		}
		if plan.QuotaResetPeriod != SubscriptionResetWeekly || plan.DurationUnit != SubscriptionDurationDay || plan.DurationValue != 30 {
			return errors.New("add-ons require a 30-day weekly-reset plan")
		}
		addon.ExpiresAt = sub.EndTime
		return tx.Create(addon).Error
	})
	return addon, err
}

// InternalAdjust reserves or reconciles an entire request atomically across the
// weekly allowance, parent-bound add-ons and the member's spending cap.
func InternalAdjust(requestID string, userID int, target int64, final bool) (*InternalCharge, error) {
	if requestID == "" || len(requestID) > 100 || target < 0 || target > 2147483647 {
		return nil, errors.New("invalid internal charge")
	}
	var out InternalCharge
	err := DB.Transaction(func(tx *gorm.DB) error {
		var record InternalCharge
		foundErr := tx.Where("request_id = ?", requestID).First(&record).Error
		existing := foundErr == nil
		if foundErr != nil && !errors.Is(foundErr, gorm.ErrRecordNotFound) {
			return foundErr
		}
		ownerID, member, err := InternalOwner(tx, userID)
		if existing {
			if record.UserID != userID {
				return errors.New("request ownership mismatch")
			}
			ownerID = record.OwnerID
			err = nil
		}
		if err != nil {
			return err
		}
		// Updates to the owner row also serialize SQLite's deferred transactions.
		result := tx.Model(&User{}).Where("id = ? AND status = ?", ownerID, common.UserStatusEnabled).UpdateColumn("id", gorm.Expr("id"))
		if result.Error != nil {
			return result.Error
		}

		if !existing || target > record.Amount {
			if err = internalLockUser(tx, ownerID); err != nil {
				return err
			}
			if err = internalLockUser(tx, userID); err != nil {
				return err
			}
		}
		// Re-read after the owner lock to make retries and duplicate request IDs safe.
		foundErr = lockForUpdate(tx).Where("request_id = ?", requestID).First(&record).Error
		existing = foundErr == nil
		if existing && record.UserID != userID {
			return errors.New("request ownership mismatch")
		}
		if existing && (record.Status == "settled" || record.Status == "refunded") {
			if target != record.Amount && target != 0 {
				return errors.New("charge is already finalized")
			}
			out = record
			return nil
		}
		var sub UserSubscription
		now := GetDBTimestamp(tx)
		if existing {
			if err = lockForUpdate(tx).First(&sub, record.SubscriptionID).Error; err != nil {
				return err
			}
		} else {
			if err = lockForUpdate(tx).Where("user_id = ? AND status = ? AND start_time <= ? AND end_time > ?", ownerID, "active", now, now).Order("end_time asc, id asc").First(&sub).Error; err != nil {
				return errors.New("active Codex subscription required")
			}
			record = InternalCharge{RequestID: requestID, UserID: userID, OwnerID: ownerID, SubscriptionID: sub.Id, Status: "reserved", CreatedAt: now, Allocations: "[]"}
			if member != nil {
				record.MemberID = member.ID
				record.TeamID = member.TeamID
			}
		}
		plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
		if err != nil {
			return err
		}
		if err = maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
			return err
		}
		if !existing {
			record.Period = sub.LastResetTime
		}
		delta := target - record.Amount
		var allocations []internalAllocation
		if err = common.Unmarshal([]byte(record.Allocations), &allocations); err != nil {
			return err
		}
		if delta > 0 {
			if sub.Status != "active" || sub.EndTime <= now {
				return errors.New("subscription expired")
			}
			currentOwner, currentMember, accessErr := InternalOwner(lockForUpdate(tx), userID)
			if accessErr != nil {
				return accessErr
			}
			if currentOwner != ownerID {
				return errors.New("billing ownership changed")
			}
			if currentMember != nil {
				if err = lockForUpdate(tx).First(&currentMember, currentMember.ID).Error; err != nil {
					return err
				}
				if !currentMember.Active {
					return errors.New("team membership disabled")
				}
				if currentMember.SubscriptionID != sub.Id {
					currentMember.QuotaUsed = 0
					currentMember.SubscriptionID = sub.Id
				}
				if currentMember.QuotaLimit > 0 && currentMember.QuotaUsed+delta > currentMember.QuotaLimit {
					return errors.New("member quota insufficient")
				}
				currentMember.QuotaUsed += delta
				if err = tx.Save(currentMember).Error; err != nil {
					return err
				}
			}
			remaining := delta
			if record.Period == sub.LastResetTime {
				available := sub.AmountTotal - sub.AmountUsed
				if sub.AmountTotal == 0 {
					available = remaining
				}
				if available > 0 {
					take := min(available, remaining)
					sub.AmountUsed += take
					record.BaseAmount += take
					remaining -= take
				}
			}
			var addons []InternalAddon
			if err = lockForUpdate(tx).Where("subscription_id = ? AND expires_at > ?", sub.Id, now).Order("id").Find(&addons).Error; err != nil {
				return err
			}
			for _, addon := range addons {
				take := min(max(int64(0), addon.AmountTotal-addon.AmountUsed), remaining)
				if take > 0 {
					addon.AmountUsed += take
					allocations = append(allocations, internalAllocation{AddonID: addon.ID, Amount: take})
					if err = tx.Save(&addon).Error; err != nil {
						return err
					}
					remaining -= take
				}
				if remaining == 0 {
					break
				}
			}
			if remaining > 0 {
				return errors.New("subscription and add-on quota insufficient")
			}
		} else if delta < 0 {
			remaining := -delta
			for i := len(allocations) - 1; i >= 0 && remaining > 0; i-- {
				take := min(allocations[i].Amount, remaining)
				if take == 0 {
					continue
				}
				var addon InternalAddon
				if err = lockForUpdate(tx).First(&addon, allocations[i].AddonID).Error; err != nil {
					return err
				}
				addon.AmountUsed = max(int64(0), addon.AmountUsed-take)
				if err = tx.Save(&addon).Error; err != nil {
					return err
				}
				allocations[i].Amount -= take
				remaining -= take
			}
			baseRefund := min(record.BaseAmount, remaining)
			record.BaseAmount -= baseRefund
			if record.Period == sub.LastResetTime {
				sub.AmountUsed = max(int64(0), sub.AmountUsed-baseRefund)
			}
			remaining -= baseRefund
			if remaining != 0 {
				return errors.New("charge allocation mismatch")
			}
			if record.MemberID > 0 {
				var priorMember InternalMember
				if err = lockForUpdate(tx).First(&priorMember, record.MemberID).Error; err != nil {
					return err
				}
				if priorMember.SubscriptionID == sub.Id {
					priorMember.QuotaUsed = max(int64(0), priorMember.QuotaUsed+delta)
					if err = tx.Save(&priorMember).Error; err != nil {
						return err
					}
				}
			}
		}
		if err = tx.Save(&sub).Error; err != nil {
			return err
		}
		encoded, err := common.Marshal(allocations)
		if err != nil {
			return err
		}
		record.Allocations = string(encoded)
		record.Amount = target
		if final {
			record.Status = "settled"
			if target == 0 {
				record.Status = "refunded"
			}
		}
		if existing {
			err = tx.Save(&record).Error
		} else {
			err = tx.Create(&record).Error
		}
		out = record
		return err
	})
	return &out, err
}

func CheckInternalSinglePlan(tx *gorm.DB, userID int) error {
	if err := internalLockUser(tx, userID); err != nil {
		return err
	}
	owner, _, err := InternalOwner(tx, userID)
	if err != nil {
		return err
	}
	if owner != userID {
		return errors.New("team members cannot hold a personal subscription")
	}
	var count int64
	if err = tx.Model(&UserSubscription{}).Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", GetDBTimestamp(tx)).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("only one active primary subscription is allowed")
	}
	return nil
}

func InternalSummary(userID int) (map[string]any, error) {
	owner, member, err := InternalOwner(DB, userID)
	if err != nil {
		return nil, err
	}
	var subs []UserSubscription
	if err = DB.Where("user_id = ? AND status = ? AND end_time > ?", owner, "active", common.GetTimestamp()).Find(&subs).Error; err != nil {
		return nil, err
	}
	var addons []InternalAddon
	for _, sub := range subs {
		var rows []InternalAddon
		if err = DB.Where("subscription_id = ?", sub.Id).Find(&rows).Error; err != nil {
			return nil, err
		}
		addons = append(addons, rows...)
	}
	result := map[string]any{"owner_id": owner, "membership": member, "subscriptions": subs, "addons": addons, "rpm": 40, "concurrency": 1}
	if member != nil {
		var team InternalTeam
		if err = DB.First(&team, member.TeamID).Error; err != nil {
			return nil, err
		}
		result["team"] = team
		if team.OwnerID == userID {
			var members []InternalMember
			if err = DB.Where("team_id = ?", team.ID).Find(&members).Error; err != nil {
				return nil, err
			}
			result["members"] = members
		}
	}
	return result, nil
}
