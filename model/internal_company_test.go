package model

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestInternalCompanyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "company.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN required")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN required")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			if dialect != "sqlite" {
				sqlDB.SetMaxOpenConns(12)
			}
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			originalDB, originalType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() { DB = originalDB; common.SetMainDatabaseType(originalType); initCol() })
			t.Setenv("INTERNAL_MODE_ENABLED", "true")
			if dialect == "postgres" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var expected int64
					if err := tx.Raw("SELECT FLOOR(EXTRACT(EPOCH FROM NOW()))::bigint").Scan(&expected).Error; err != nil {
						return err
					}
					assert.Equal(t, expected, GetDBTimestamp(tx), "database timestamps must truncate fractional Unix seconds")
					return nil
				}))
			}
			tables := []any{&User{}, &UserSession{}, &UserAccessToken{}, &SubscriptionPlan{}, &UserSubscription{}, &InternalInvite{}, &InternalTeam{}, &InternalMember{}, &InternalAddon{}, &InternalCharge{}}
			// A legacy database is upgraded twice, preserving the existing user.
			require.NoError(t, db.AutoMigrate(&User{}))
			owner := User{Username: "owner", Password: "fixture-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 12345}
			require.NoError(t, db.Create(&owner).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(tables...))
			}
			t.Cleanup(func() {
				for i := len(tables) - 1; i >= 0; i-- {
					require.NoError(t, db.Migrator().DropTable(tables[i]))
				}
			})
			var preserved User
			require.NoError(t, db.First(&preserved, owner.Id).Error)
			assert.Equal(t, 12345, preserved.Quota)
			invite, token, err := NewInternalInvite(owner.Id, 0)
			require.NoError(t, err)
			assert.NotContains(t, invite.Hash, token)
			var raw string
			require.NoError(t, db.Table("internal_invites").Select("hash").Where("id = ?", invite.ID).Scan(&raw).Error)
			assert.NotEqual(t, token, raw)
			registered := User{Username: "invited", Password: "correct horse battery staple", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, RegisterInternalUser(&registered, token))
			assert.Zero(t, registered.Quota)
			reused := User{Username: "reuse", Password: "correct horse battery staple", Role: common.RoleCommonUser}
			require.Error(t, RegisterInternalUser(&reused, token))
			expired, expiredToken, err := NewInternalInvite(owner.Id, 0)
			require.NoError(t, err)
			require.NoError(t, db.Model(expired).Update("expires_at", GetDBTimestamp()-1).Error)
			require.Error(t, RegisterInternalUser(&reused, expiredToken))
			require.Error(t, RegisterInternalUser(&reused, "bad-token"))
			recovery, recoveryToken, err := NewInternalInvite(owner.Id, registered.Id)
			require.NoError(t, err)
			assert.Equal(t, "reset", recovery.Kind)
			var old User
			require.NoError(t, db.First(&old, registered.Id).Error)
			require.NoError(t, ResetInternalPassword(recoveryToken, "a brand new long password"))
			require.Error(t, ResetInternalPassword(recoveryToken, "another new long password"))
			var changed User
			require.NoError(t, db.First(&changed, registered.Id).Error)
			assert.Greater(t, changed.AuthVersion, old.AuthVersion)
			assert.NotEqual(t, old.Password, changed.Password)
			// Invite consumption and insertion roll back together on username conflicts.
			rollbackInvite, rollbackToken, err := NewInternalInvite(owner.Id, 0)
			require.NoError(t, err)
			conflict := User{Username: "owner", Password: "correct horse battery staple", Role: common.RoleCommonUser}
			require.Error(t, RegisterInternalUser(&conflict, rollbackToken))
			require.NoError(t, db.First(rollbackInvite, rollbackInvite.ID).Error)
			assert.Zero(t, rollbackInvite.UsedAt)
			weekly := SubscriptionPlan{Title: "weekly", Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 30, QuotaResetPeriod: SubscriptionResetWeekly, TotalAmount: 100, AllowWalletOverflow: common.GetPointer(false)}
			require.NoError(t, db.Create(&weekly).Error)
			var sub *UserSubscription
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				sub, e = CreateUserSubscriptionFromPlanTx(tx, owner.Id, &weekly, "test")
				return e
			}))
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := CreateUserSubscriptionFromPlanTx(tx, owner.Id, &weekly, "test")
				return e
			}))
			addon, err := GrantInternalAddon(sub.Id, 5000000, owner.Id)
			require.NoError(t, err)
			assert.Equal(t, sub.EndTime, addon.ExpiresAt)
			_, err = InternalAdjust("personal", owner.Id, 120, false)
			require.NoError(t, err)
			require.NoError(t, db.First(&sub, sub.Id).Error)
			assert.EqualValues(t, 100, sub.AmountUsed)
			require.NoError(t, db.First(addon, addon.ID).Error)
			assert.EqualValues(t, 20, addon.AmountUsed)
			_, err = InternalAdjust("personal", owner.Id, 110, true)
			require.NoError(t, err)
			require.NoError(t, db.First(addon, addon.ID).Error)
			assert.EqualValues(t, 10, addon.AmountUsed)
			_, err = InternalAdjust("personal", owner.Id, 110, true)
			require.NoError(t, err)
			// Team membership uses the owner pool; per-member caps and removals are enforced.
			team, err := CreateInternalTeam("Engineering", owner.Id)
			require.NoError(t, err)
			require.NoError(t, SetInternalMember(owner.Id, team.ID, registered.Id, 50, true, false))
			_, err = InternalAdjust("member", registered.Id, 40, false)
			require.NoError(t, err)
			_, err = InternalAdjust("over-cap", registered.Id, 11, false)
			require.Error(t, err)
			require.NoError(t, SetInternalMember(owner.Id, team.ID, registered.Id, 50, false, false))
			_, err = InternalAdjust("removed", registered.Id, 1, false)
			require.Error(t, err)
			// Refunds still return to the original source after member removal.
			_, err = InternalAdjust("member", registered.Id, 0, true)
			require.NoError(t, err)
			_, err = InternalAdjust("member", registered.Id, 0, true)
			require.NoError(t, err)
			var member InternalMember
			require.NoError(t, db.Where("user_id = ?", registered.Id).First(&member).Error)
			assert.Zero(t, member.QuotaUsed)
			require.Error(t, SetInternalMember(registered.Id, team.ID, registered.Id, 100, true, false))
			require.NoError(t, SetInternalMember(owner.Id, team.ID, registered.Id, 0, true, false))
			// The last available units cannot be overspent by parallel members.
			require.NoError(t, db.Model(addon).Update("amount_used", addon.AmountTotal).Error)
			require.NoError(t, db.Model(sub).Update("amount_used", 90).Error)
			var successes atomic.Int32
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, e := InternalAdjust(string(rune('a'+i)), registered.Id, 10, true)
					if e == nil {
						successes.Add(1)
					}
				}(i)
			}
			wg.Wait()
			assert.EqualValues(t, 1, successes.Load())
			require.NoError(t, db.First(&sub, sub.Id).Error)
			assert.EqualValues(t, 100, sub.AmountUsed)
			// A late refund cannot create extra quota in the new weekly period.
			require.NoError(t, db.Model(sub).Updates(map[string]any{"amount_used": 0, "last_reset_time": GetDBTimestamp() - 100, "next_reset_time": GetDBTimestamp() + 10000}).Error)
			_, err = InternalAdjust("old-period", owner.Id, 20, false)
			require.NoError(t, err)
			require.NoError(t, db.Model(sub).Updates(map[string]any{"amount_used": 30, "last_reset_time": GetDBTimestamp(), "next_reset_time": GetDBTimestamp() + 10000}).Error)
			_, err = InternalAdjust("old-period", owner.Id, 0, true)
			require.NoError(t, err)
			require.NoError(t, db.First(&sub, sub.Id).Error)
			assert.EqualValues(t, 30, sub.AmountUsed)
			require.NoError(t, db.Model(sub).Update("end_time", GetDBTimestamp()-1).Error)
			_, err = InternalAdjust("expired", owner.Id, 1, false)
			require.Error(t, err)
			_, err = GrantInternalAddon(sub.Id, 5000000, owner.Id)
			require.Error(t, err)
		})
	}
}
