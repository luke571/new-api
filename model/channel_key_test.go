package model

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestEncryptedChannelKeysDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&Channel{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Channel{})) })
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldType); initCol() })
			t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", "")
			tag := "legacy-tag"
			legacy := Channel{Name: "legacy", Key: "sk-legacy-sensitive", Models: "storage-test", Group: "default", Tag: &tag}
			require.NoError(t, db.Create(&legacy).Error)
			storageKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))
			t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", storageKey)
			for range 2 {
				require.NoError(t, MigrateChannelKeys(db))
			}
			var raw string
			require.NoError(t, db.Table("channels").Select(commonKeyColumn(db)).Where("id = ?", legacy.Id).Scan(&raw).Error)
			require.NotContains(t, raw, legacy.Key)
			require.Contains(t, raw, channelKeyPrefix)
			beforeRestart := raw
			require.NoError(t, MigrateChannelKeys(db))
			require.NoError(t, db.Table("channels").Select(commonKeyColumn(db)).Where("id = ?", legacy.Id).Scan(&raw).Error)
			assert.Equal(t, beforeRestart, raw)
			loaded, err := GetChannelById(legacy.Id, true)
			require.NoError(t, err)
			assert.Equal(t, legacy.Key, loaded.Key)
			found, err := SearchChannels(legacy.Key, "default", "storage-test", true)
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, legacy.Id, found[0].Id)
			tags, err := SearchTags(legacy.Key, "default", "storage-test", true)
			require.NoError(t, err)
			require.Len(t, tags, 1)
			assert.Equal(t, "legacy-tag", *tags[0])
			for i, secret := range []string{"key-one\nkey-two", `{"access_token":"secret-access","refresh_token":"secret-refresh"}`} {
				channel := Channel{Name: fmt.Sprint("new-", i), Key: secret, Models: "storage-test"}
				require.NoError(t, db.Create(&channel).Error)
				require.NoError(t, db.Table("channels").Select(commonKeyColumn(db)).Where("id = ?", channel.Id).Scan(&raw).Error)
				assert.NotContains(t, raw, secret)
				loaded, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, secret, loaded.Key)
				require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Update("key", "refreshed-secret").Error)
				loaded, err = GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, "refreshed-secret", loaded.Key)
				require.NoError(t, db.Table("channels").Select(commonKeyColumn(db)).Where("id = ?", channel.Id).Scan(&raw).Error)
				assert.NotContains(t, raw, "refreshed-secret")
			}
			t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", "")
			require.Error(t, MigrateChannelKeys(db))
			_, err = GetChannelById(legacy.Id, true)
			require.Error(t, err)
			t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{18}, 32)))
			require.Error(t, MigrateChannelKeys(db))
			_, err = GetChannelById(legacy.Id, true)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), legacy.Key)
			t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", storageKey)
			assert.NoError(t, MigrateChannelKeys(db))
		})
	}
}

func TestChannelKeyEncryptionRejectsTampering(t *testing.T) {
	t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)))
	first, err := sealChannelKey("confidential-key")
	require.NoError(t, err)
	second, err := sealChannelKey("confidential-key")
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "equal keys must use independent random nonces")
	for _, malformed := range []string{first[:len(first)-3], "newapi-key:v2:unknown", first[:len(channelKeyPrefix)] + "0" + first[len(channelKeyPrefix)+1:]} {
		_, err := openChannelKey(malformed)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "confidential-key")
	}
	t.Setenv("CHANNEL_KEY_ENCRYPTION_KEY", "invalid-key")
	_, err = sealChannelKey("confidential-key")
	require.Error(t, err)
}
