package model

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const channelKeyPrefix = "newapi-key:v1:"

func init() {
	schema.RegisterSerializer("channel_key", channelKeySerializer{})
}

// The storage key is independent of sessions and must be backed up separately
// from the database. Removing or changing it must never expose ciphertext as a key.
func channelKeyMaster() ([]byte, error) {
	encoded := os.Getenv("CHANNEL_KEY_ENCRYPTION_KEY")
	if encoded == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("CHANNEL_KEY_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}

func channelKeyDerive(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("new-api/channel-key/v1/" + purpose))
	return mac.Sum(nil)
}

func channelKeyFingerprint(master []byte, value string) string {
	mac := hmac.New(sha256.New, channelKeyDerive(master, "search"))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func channelKeyAEAD(master []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(channelKeyDerive(master, "encryption"))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealChannelKey(value string) (string, error) {
	master, err := channelKeyMaster()
	if err != nil {
		return "", err
	}
	if len(master) == 0 || value == "" {
		return value, nil
	}
	aead, err := channelKeyAEAD(master)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate channel key nonce: %w", err)
	}
	header := channelKeyPrefix + channelKeyFingerprint(master, value) + ":"
	ciphertext := aead.Seal(nonce, nonce, []byte(value), []byte(header))
	return header + base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

func openChannelKey(value string) (string, error) {
	if !strings.HasPrefix(value, "newapi-key:") {
		return value, nil
	}
	master, err := channelKeyMaster()
	if err != nil {
		return "", err
	}
	if len(master) == 0 {
		return "", errors.New("CHANNEL_KEY_ENCRYPTION_KEY is required to read encrypted channel keys")
	}
	if !strings.HasPrefix(value, channelKeyPrefix) {
		return "", errors.New("unsupported encrypted channel key version")
	}
	fingerprint, payload, ok := strings.Cut(strings.TrimPrefix(value, channelKeyPrefix), ":")
	if !ok || len(fingerprint) != 64 {
		return "", errors.New("invalid encrypted channel key")
	}
	aead, err := channelKeyAEAD(master)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawStdEncoding.Strict().DecodeString(payload)
	if err != nil || len(ciphertext) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("invalid encrypted channel key")
	}
	header := channelKeyPrefix + fingerprint + ":"
	plaintext, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], []byte(header))
	if err != nil {
		return "", errors.New("channel key decryption failed; verify CHANNEL_KEY_ENCRYPTION_KEY")
	}
	if !hmac.Equal([]byte(fingerprint), []byte(channelKeyFingerprint(master, string(plaintext)))) {
		return "", errors.New("invalid encrypted channel key fingerprint")
	}
	return string(plaintext), nil
}

type channelKeySerializer struct{}

func (channelKeySerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	var value string
	switch raw := dbValue.(type) {
	case nil:
	case string:
		value = raw
	case []byte:
		value = string(raw)
	default:
		return errors.New("invalid channel key database value")
	}
	plaintext, err := openChannelKey(value)
	if err != nil {
		return err
	}
	return field.Set(ctx, dst, plaintext)
}

func (channelKeySerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	if fieldValue == nil {
		return "", nil
	}
	value, ok := fieldValue.(string)
	if !ok {
		return nil, errors.New("invalid channel key value")
	}
	return sealChannelKey(value)
}

// Keep exact-secret administrator searches working without deterministic encryption.
func channelKeySearch(value string) (string, string, error) {
	master, err := channelKeyMaster()
	if err != nil {
		return "", "", err
	}
	if len(master) == 0 || value == "" {
		return " = ?", value, nil
	}
	return " LIKE ?", channelKeyPrefix + channelKeyFingerprint(master, value) + ":%", nil
}

// MigrateChannelKeys upgrades legacy plaintext in place without a schema change.
// Conditional updates preserve concurrent edits. A transaction makes a failed
// decryption leave the original database intact. Repeated startups are idempotent.
func MigrateChannelKeys(db *gorm.DB) error {
	master, err := channelKeyMaster()
	if err != nil {
		return err
	}
	if len(master) == 0 {
		var count int64
		if err := db.Table("channels").Where(commonKeyColumn(db)+" LIKE ?", "newapi-key:%").Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("CHANNEL_KEY_ENCRYPTION_KEY is required for this database")
		}
		return nil
	}
	// Legacy values appear in conditional UPDATE predicates; never log those SQL arguments.
	return db.Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		lastID := 0
		for {
			var rows []struct {
				ID  int
				Key string
			}
			if err := tx.Table("channels").Select("id", "key").Where("id > ?", lastID).Order("id").Limit(100).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				return nil
			}
			for _, row := range rows {
				lastID = row.ID
				if strings.HasPrefix(row.Key, "newapi-key:") {
					if _, err := openChannelKey(row.Key); err != nil {
						return err
					}
					continue
				}
				if row.Key == "" {
					continue
				}
				encrypted, err := sealChannelKey(row.Key)
				if err != nil {
					return err
				}
				if err := tx.Table("channels").Where("id = ? AND "+commonKeyColumn(tx)+" = ?", row.ID, row.Key).Update("key", encrypted).Error; err != nil {
					return err
				}
			}
		}
	})
}

func commonKeyColumn(db *gorm.DB) string {
	return db.Statement.Quote("key")
}

// GORM applies serializers to struct writes, but map-based updates bypass them.
// Encrypt those updates here as well, including credential refresh operations.
func (channel *Channel) BeforeSave(tx *gorm.DB) error {
	values, ok := tx.Statement.Dest.(map[string]any)
	if !ok {
		return nil
	}
	for _, name := range []string{"key", "Key"} {
		value, exists := values[name]
		if !exists {
			continue
		}
		plaintext, ok := value.(string)
		if !ok {
			return errors.New("channel key updates require a string value")
		}
		encrypted, err := sealChannelKey(plaintext)
		if err != nil {
			return err
		}
		values[name] = encrypted
	}
	return nil
}
