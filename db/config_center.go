package db

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/caitun/inferkit/services/configcrypt"
)

const (
	configLockUpdate = "UPDATE"
	// MaxConfigBytes exposes the package's max config bytes value.
	MaxConfigBytes = 512 * 1024
	// MaxConfigDescriptionBytes exposes the package's max config description bytes value.
	MaxConfigDescriptionBytes    = 512 * 1024 //nolint:goconst // Config and description payload limits are independently configurable.
	maxConfigDepth               = 32
	maxConfigNodes               = 5000
	maxConfigCollection          = 1000
	maxConfigKeyLength           = 128
	maxConfigStringLength        = 64 * 1024
	maxConfigDescriptionLength   = 2000
	maxConfigReleaseReasonLength = 1000 //nolint:goconst // Release-reason and collection limits enforce separate domain constraints.
	maxConfigEnvironments        = 100
	maxConfigNamespaces          = 50
	minConfigAPIKeyLength        = 32
	maxConfigAPIKeyLength        = 256
	maxEnvironmentDepth          = 16
	configReencryptBatchSize     = 100 //nolint:goconst // Re-encryption batches and environment capacity are tuned independently.
	payloadDraftConfig           = "draft-config"
	payloadDraftDescriptions     = "draft-descriptions"
	payloadReleaseConfig         = "release-config"
	payloadReleaseDescriptions   = "release-descriptions"
	payloadNamespaceAPIKey       = "namespace-api-key" // #nosec G101 -- this is an encryption payload label, not a credential.
	columnDraftConfig            = "draft_config"
	columnDraftDescriptions      = "draft_descriptions"
)

var (
	// ErrConfigNamespaceNotFound indicates config namespace not found.
	ErrConfigNamespaceNotFound = errors.New("config namespace not found")
	// ErrConfigNamespaceInvalid indicates invalid config namespace.
	ErrConfigNamespaceInvalid = errors.New("invalid config namespace")
	// ErrConfigNamespaceConflict indicates config namespace conflicts with existing data.
	ErrConfigNamespaceConflict = errors.New("config namespace conflicts with existing data")
	// ErrConfigAPIKeyInvalid indicates invalid config namespace API key.
	ErrConfigAPIKeyInvalid = errors.New("invalid config namespace API key")
	// ErrConfigAPIKeyUnauthorized indicates config namespace API key is unauthorized.
	ErrConfigAPIKeyUnauthorized = errors.New("config namespace API key is unauthorized")
	// ErrConfigEncryptionRequired indicates config encryption is required.
	ErrConfigEncryptionRequired = errors.New("config encryption is required")
	// ErrConfigEnvironmentNotFound indicates config environment not found.
	ErrConfigEnvironmentNotFound = errors.New("config environment not found")
	// ErrConfigInvalid indicates invalid configuration.
	ErrConfigInvalid = errors.New("invalid configuration")
	// ErrConfigEnvironmentInvalid indicates invalid config environment.
	ErrConfigEnvironmentInvalid = errors.New("invalid config environment")
	// ErrConfigEnvironmentConflict indicates config environment conflicts with existing data.
	ErrConfigEnvironmentConflict = errors.New("config environment conflicts with existing data")
	// ErrConfigEnvironmentInUse indicates config environment is inherited by another environment.
	ErrConfigEnvironmentInUse = errors.New("config environment is inherited by another environment")
	// ErrConfigReleaseNotFound indicates published configuration not found.
	ErrConfigReleaseNotFound = errors.New("published configuration not found")
	// ErrConfigReleaseInvalid indicates invalid config release.
	ErrConfigReleaseInvalid = errors.New("invalid config release")
	// ErrConfigStorage indicates config storage operation failed.
	ErrConfigStorage               = errors.New("config storage operation failed")
	errConfigReleaseReasonRequired = errors.New("config release reason is required")
	errConfigReleaseReasonTooLong  = errors.New("config release reason is too long")
	errNamespaceLimitReached       = errors.New("namespace limit reached")
	errEnvironmentLimitReached     = errors.New("environment limit reached")
	errConfigurationTooLarge       = errors.New("configuration is too large")
	errInheritanceCycle            = errors.New("environment inheritance cycle detected")
	errInheritanceTooDeep          = errors.New("environment inheritance is too deep")
	errParentEnvironmentMissing    = errors.New("parent environment does not exist")
	errConfigSizeInvalid           = errors.New("configuration size is invalid")
	errConfigRootNotObject         = errors.New("configuration root must be an object")
	errConfigMultipleJSONValues    = errors.New("configuration contains multiple JSON values")
	errDescriptionsTooLarge        = errors.New("configuration descriptions are too large")
	errDescriptionsMultipleValues  = errors.New("configuration descriptions contain multiple JSON values")
	errTooManyDescriptions         = errors.New("configuration contains too many descriptions")
	errDescriptionTooLong          = errors.New("configuration description is too long")
	errDescriptionPathInvalid      = errors.New("configuration description path is invalid")
	errConfigNestingTooDeep        = errors.New("configuration nesting is too deep")
	errTooManyConfigValues         = errors.New("configuration contains too many values")
	errTooManyObjectFields         = errors.New("configuration object contains too many fields")
	errConfigKeyInvalid            = errors.New("configuration key is invalid")
	errTooManyArrayItems           = errors.New("configuration array contains too many items")
	errConfigStringTooLong         = errors.New("configuration string is too long")
	errUnsupportedConfigValue      = errors.New("configuration contains an unsupported value type")
	errJSONPointerItemRequired     = errors.New("JSON pointer must identify a configuration item")
	errJSONPointerInvalidEscape    = errors.New("JSON pointer contains an invalid escape")
	errNamespaceNameInvalid        = errors.New("namespace name must contain 1 to 100 characters")
	errNamespaceSlugInvalid        = errors.New("namespace slug must start with a letter and contain only lowercase letters, numbers, hyphens, or underscores")
	errNamespaceDescriptionLong    = errors.New("namespace description is too long")
	errNamespaceAPIKeyRequired     = errors.New("namespace API key is required")
	errNamespaceAPIKeyLength       = errors.New("namespace API key must contain 32 to 256 URL-safe characters")
	errEnvironmentNameInvalid      = errors.New("environment name must contain 1 to 100 characters")
	errEnvironmentSlugInvalid      = errors.New("environment slug must start with a letter and contain only lowercase letters, numbers, hyphens, or underscores")
	errEnvironmentDescriptionLong  = errors.New("environment description is too long")
	errSelfInheritance             = errors.New("an environment cannot inherit from itself")

	configSlugPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	configAPIKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

// ConfigNamespace is the top-level boundary for a group of configuration
// environments. Deleting it cascades to environments and their releases.
type ConfigNamespace struct {
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Name             string    `gorm:"size:100;not null" json:"name"`
	Slug             string    `gorm:"size:64;not null;uniqueIndex" json:"slug"`
	Description      string    `gorm:"size:500;not null;default:''" json:"description"`
	APIKey           string    `gorm:"column:api_key;type:mediumtext;not null" json:"-"`
	ID               uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	EnvironmentCount int64     `gorm:"-" json:"environment_count"`
	APIKeyConfigured bool      `gorm:"-" json:"api_key_configured"`
}

// ConfigEnvironment stores only the overrides owned by an environment. The
// final configuration is resolved by merging each ancestor from root to leaf.
type ConfigEnvironment struct {
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Name              string    `gorm:"size:100;not null" json:"name"`
	Slug              string    `gorm:"size:64;not null;uniqueIndex:idx_config_environment_namespace_slug" json:"slug"`
	Description       string    `gorm:"size:500;not null;default:''" json:"description"`
	DraftConfig       string    `gorm:"type:mediumtext;not null" json:"-"`
	DraftDescriptions string    `gorm:"type:mediumtext;not null" json:"-"`
	ID                uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	NamespaceID       uint      `gorm:"not null;uniqueIndex:idx_config_environment_namespace_slug" json:"namespace_id"`
	ParentID          uint      `gorm:"not null;default:0;index" json:"parent_id"`
	Version           uint64    `gorm:"-" json:"published_version"`
	HasDraft          bool      `gorm:"-" json:"has_draft"`
}

// ConfigRelease is an immutable, fully-resolved configuration snapshot.
type ConfigRelease struct {
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	BatchID       string    `gorm:"size:32;not null;index" json:"batch_id"`
	Reason        string    `gorm:"size:1000;not null;default:''" json:"reason"`
	Config        string    `gorm:"type:mediumtext;not null" json:"-"`
	Descriptions  string    `gorm:"type:mediumtext;not null" json:"-"`
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	EnvironmentID uint      `gorm:"not null;uniqueIndex:idx_config_release_version" json:"environment_id"`
	Version       uint64    `gorm:"not null;uniqueIndex:idx_config_release_version" json:"version"`
}

// ConfigEnvironmentInput represents config environment input data.
type ConfigEnvironmentInput struct {
	Name        string
	Slug        string
	Description string
	ParentID    uint
}

// ConfigNamespaceInput represents config namespace input data.
type ConfigNamespaceInput struct {
	Name        string
	Slug        string
	Description string
	APIKey      string
}

// ResolvedConfig represents resolved config data.
type ResolvedConfig struct {
	Config       map[string]any      `json:"config"`
	Descriptions ConfigDescriptions  `json:"descriptions"`
	Chain        []ConfigEnvironment `json:"inheritance_chain"`
	Environment  ConfigEnvironment   `json:"environment"`
}

// PublishedConfig represents published config data.
type PublishedConfig struct {
	PublishedAt   time.Time          `json:"published_at"`
	Config        map[string]any     `json:"config"`
	Descriptions  ConfigDescriptions `json:"descriptions"`
	BatchID       string             `json:"batch_id"`
	Reason        string             `json:"reason"`
	EnvironmentID uint               `json:"environment_id"`
	Version       uint64             `json:"version"`
}

// ConfigDescriptions represents config descriptions data.
type ConfigDescriptions map[string]string

// ConfigReleaseSummary represents config release summary data.
type ConfigReleaseSummary struct {
	PublishedAt   time.Time `json:"published_at"`
	BatchID       string    `json:"batch_id"`
	Reason        string    `json:"reason"`
	EnvironmentID uint      `json:"environment_id"`
	Version       uint64    `json:"version"`
}

// ConfigPublishResult represents config publish result data.
type ConfigPublishResult struct {
	BatchID  string            `json:"batch_id"`
	Reason   string            `json:"reason"`
	Releases []PublishedConfig `json:"releases"`
}

// ConfigPublishInput represents config publish input data.
type ConfigPublishInput struct {
	Reason         string
	EnvironmentIDs []uint
}

// ConfigReencryptResult represents config reencrypt result data.
type ConfigReencryptResult struct {
	NamespaceRecords   int64
	EnvironmentRecords int64
	ReleaseRecords     int64
	Payloads           int64
}

// ListConfigNamespaces lists config namespaces.
func ListConfigNamespaces(ctx context.Context) ([]ConfigNamespace, error) {
	namespaces := make([]ConfigNamespace, 0)
	if err := DB().WithContext(ctx).Order("name ASC, id ASC").Find(&namespaces).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	counts := make([]struct {
		NamespaceID uint
		Count       int64
	}, 0)
	if err := DB().WithContext(ctx).
		Model(&ConfigEnvironment{}).
		Select("namespace_id, COUNT(*) AS count").
		Group("namespace_id").
		Scan(&counts).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	countByNamespace := make(map[uint]int64, len(counts))
	for _, count := range counts {
		countByNamespace[count.NamespaceID] = count.Count
	}
	for index := range namespaces {
		namespaces[index].EnvironmentCount = countByNamespace[namespaces[index].ID]
		namespaces[index].APIKeyConfigured = namespaces[index].APIKey != ""
	}
	return namespaces, nil
}

// CreateConfigNamespace exposes the package's create config namespace value.
//
//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func CreateConfigNamespace(ctx context.Context, input ConfigNamespaceInput) (*ConfigNamespace, error) {
	input = normalizeConfigNamespaceInput(input)
	if err := validateConfigNamespaceInput(input, true); err != nil {
		return nil, err
	}
	created := &ConfigNamespace{
		Name:        input.Name,
		Slug:        input.Slug,
		Description: input.Description,
	}
	err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&ConfigNamespace{}).Count(&count).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if count >= maxConfigNamespaces {
			return errors.Join(ErrConfigNamespaceInvalid, errNamespaceLimitReached)
		}
		var existing int64
		if err := tx.Model(&ConfigNamespace{}).Where("slug = ?", input.Slug).Count(&existing).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if existing > 0 {
			return ErrConfigNamespaceConflict
		}
		if err := tx.Create(created).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		storedAPIKey, err := encryptNamespaceAPIKey(input.APIKey, created.ID)
		if err != nil {
			return err
		}
		created.APIKey = storedAPIKey
		created.APIKeyConfigured = true
		if updateErr := tx.Model(created).UpdateColumn("api_key", storedAPIKey).Error; updateErr != nil {
			return errors.Join(ErrConfigStorage, updateErr)
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return created, nil
}

// UpdateConfigNamespace exposes the package's update config namespace value.
//
//nolint:cyclop,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func UpdateConfigNamespace(ctx context.Context, id uint, input ConfigNamespaceInput) (*ConfigNamespace, error) {
	input = normalizeConfigNamespaceInput(input)
	if id == 0 {
		return nil, ErrConfigNamespaceNotFound
	}
	if err := validateConfigNamespaceInput(input, false); err != nil {
		return nil, err
	}
	updated := &ConfigNamespace{}
	err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var namespace ConfigNamespace
		if err := tx.Clauses(clause.Locking{Strength: configLockUpdate}).First(&namespace, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrConfigNamespaceNotFound
			}
			return errors.Join(ErrConfigStorage, err)
		}
		var existing int64
		if err := tx.Model(&ConfigNamespace{}).Where("slug = ? AND id <> ?", input.Slug, id).Count(&existing).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if existing > 0 {
			return ErrConfigNamespaceConflict
		}
		changes := map[string]any{"name": input.Name, "slug": input.Slug, "description": input.Description}
		if input.APIKey != "" {
			storedAPIKey, err := encryptNamespaceAPIKey(input.APIKey, namespace.ID)
			if err != nil {
				return err
			}
			changes["api_key"] = storedAPIKey
		}
		if err := tx.Model(&namespace).Updates(changes).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if err := tx.First(updated, id).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		updated.APIKeyConfigured = updated.APIKey != ""
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return updated, nil
}

// DeleteConfigNamespace deletes config namespace.
func DeleteConfigNamespace(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrConfigNamespaceNotFound
	}
	result := DB().WithContext(ctx).Delete(&ConfigNamespace{}, id)
	if result.Error != nil {
		return errors.Join(ErrConfigStorage, result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrConfigNamespaceNotFound
	}
	return nil
}

// ListConfigEnvironments lists config environments.
func ListConfigEnvironments(ctx context.Context, namespaceID uint) ([]ConfigEnvironment, error) {
	return listConfigEnvironments(ctx, DB(), namespaceID)
}

func listConfigEnvironments(ctx context.Context, conn *gorm.DB, namespaceID uint) ([]ConfigEnvironment, error) {
	if err := requireConfigNamespace(ctx, conn, namespaceID); err != nil {
		return nil, err
	}
	environments := make([]ConfigEnvironment, 0)
	if err := conn.WithContext(ctx).Where("namespace_id = ?", namespaceID).Order("name ASC, id ASC").Find(&environments).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}

	latestVersions := conn.WithContext(ctx).
		Model(&ConfigRelease{}).
		Select("environment_id, MAX(version) AS version").
		Group("environment_id")
	latestReleases := make([]ConfigRelease, 0)
	if err := conn.WithContext(ctx).
		Where("environment_id IN (?)", conn.Model(&ConfigEnvironment{}).Select("id").Where("namespace_id = ?", namespaceID)).
		Where("(environment_id, version) IN (?)", latestVersions).
		Find(&latestReleases).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	if err := applyConfigEnvironmentReleaseState(environments, latestReleases); err != nil {
		return nil, err
	}
	return environments, nil
}

//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func applyConfigEnvironmentReleaseState(environments []ConfigEnvironment, releases []ConfigRelease) error {
	latestByEnvironment := make(map[uint]ConfigRelease, len(releases))
	for _, release := range releases {
		latest, exists := latestByEnvironment[release.EnvironmentID]
		if !exists || release.Version > latest.Version {
			latestByEnvironment[release.EnvironmentID] = release
		}
	}
	for index := range environments {
		release, published := latestByEnvironment[environments[index].ID]
		if !published {
			environments[index].HasDraft = true
			continue
		}
		resolved, err := resolveConfigFromEnvironments(environments, environments[index].ID)
		if err != nil {
			return err
		}
		config, descriptions, err := encodeResolvedConfig(resolved)
		if err != nil {
			return err
		}
		releaseConfig, releaseDescriptions, err := decodeStoredRelease(release, environments[index].NamespaceID)
		if err != nil {
			return err
		}
		environments[index].Version = release.Version
		environments[index].HasDraft = config != releaseConfig || descriptions != releaseDescriptions
	}
	return nil
}

// CreateConfigEnvironment exposes the package's create config environment value.
//
//nolint:cyclop,funlen,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func CreateConfigEnvironment(ctx context.Context, namespaceID uint, input ConfigEnvironmentInput) (*ConfigEnvironment, error) {
	input = normalizeConfigEnvironmentInput(input)
	if namespaceID == 0 {
		return nil, ErrConfigNamespaceNotFound
	}
	if err := validateConfigEnvironmentInput(input); err != nil {
		return nil, err
	}

	var created *ConfigEnvironment
	err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := requireConfigNamespace(ctx, tx, namespaceID); err != nil {
			return err
		}
		if err := tx.Model(&ConfigEnvironment{}).Where("namespace_id = ?", namespaceID).Count(&count).Error; err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if count >= maxConfigEnvironments {
			return errors.Join(ErrConfigEnvironmentInvalid, errEnvironmentLimitReached)
		}
		environments, err := lockedConfigEnvironments(ctx, tx, namespaceID)
		if err != nil {
			return err
		}
		if validationErr := validateEnvironmentParent(environments, 0, input.ParentID); validationErr != nil {
			return validationErr
		}
		if environmentSlugExists(environments, input.Slug, 0) {
			return ErrConfigEnvironmentConflict
		}
		initialConfig, err := encryptConfigPayload([]byte("{}"), draftPayloadContext(namespaceID, 0, payloadDraftConfig))
		if err != nil {
			return err
		}
		initialDescriptions, err := encryptConfigPayload([]byte("{}"), draftPayloadContext(namespaceID, 0, payloadDraftDescriptions))
		if err != nil {
			return err
		}
		created = &ConfigEnvironment{
			Name:              input.Name,
			Slug:              input.Slug,
			Description:       input.Description,
			ParentID:          input.ParentID,
			DraftConfig:       initialConfig,
			DraftDescriptions: initialDescriptions,
			NamespaceID:       namespaceID,
		}
		if createErr := tx.Create(created).Error; createErr != nil {
			return errors.Join(ErrConfigStorage, createErr)
		}
		storedConfig, err := encryptConfigPayload([]byte("{}"), draftPayloadContext(namespaceID, created.ID, payloadDraftConfig))
		if err != nil {
			return err
		}
		storedDescriptions, err := encryptConfigPayload([]byte("{}"), draftPayloadContext(namespaceID, created.ID, payloadDraftDescriptions))
		if err != nil {
			return err
		}
		created.DraftConfig = storedConfig
		created.DraftDescriptions = storedDescriptions
		if updateErr := tx.Model(created).UpdateColumns(map[string]any{
			columnDraftConfig:       storedConfig,
			columnDraftDescriptions: storedDescriptions,
		}).Error; updateErr != nil {
			return errors.Join(ErrConfigStorage, updateErr)
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return created, nil
}

// UpdateConfigEnvironment exposes the package's update config environment value.
//
//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func UpdateConfigEnvironment(ctx context.Context, namespaceID, id uint, input ConfigEnvironmentInput) (*ConfigEnvironment, error) {
	input = normalizeConfigEnvironmentInput(input)
	if id == 0 {
		return nil, ErrConfigEnvironmentNotFound
	}
	if err := validateConfigEnvironmentInput(input); err != nil {
		return nil, err
	}

	updated := &ConfigEnvironment{}
	err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		environments, err := lockedConfigEnvironments(ctx, tx, namespaceID)
		if err != nil {
			return err
		}
		exists := configEnvironmentExists(environments, id)
		if !exists {
			return ErrConfigEnvironmentNotFound
		}
		if validationErr := validateEnvironmentParent(environments, id, input.ParentID); validationErr != nil {
			return validationErr
		}
		if environmentSlugExists(environments, input.Slug, id) {
			return ErrConfigEnvironmentConflict
		}
		changes := map[string]any{
			"name":        input.Name,
			"slug":        input.Slug,
			"description": input.Description,
			"parent_id":   input.ParentID,
		}
		if updateErr := tx.Model(&ConfigEnvironment{}).Where("namespace_id = ? AND id = ?", namespaceID, id).Updates(changes).Error; updateErr != nil {
			return errors.Join(ErrConfigStorage, updateErr)
		}
		if reloadErr := tx.Where("namespace_id = ?", namespaceID).First(updated, id).Error; reloadErr != nil {
			return errors.Join(ErrConfigStorage, reloadErr)
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return updated, nil
}

// DeleteConfigEnvironment exposes the package's delete config environment value.
//
//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func DeleteConfigEnvironment(ctx context.Context, namespaceID, id uint) error {
	if id == 0 {
		return ErrConfigEnvironmentNotFound
	}
	err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		environments, err := lockedConfigEnvironments(ctx, tx, namespaceID)
		if err != nil {
			return err
		}
		if !configEnvironmentExists(environments, id) {
			return ErrConfigEnvironmentNotFound
		}
		for _, environment := range environments {
			if environment.ParentID == id {
				return ErrConfigEnvironmentInUse
			}
		}
		if releasesDeleteErr := tx.Where("environment_id = ?", id).Delete(&ConfigRelease{}).Error; releasesDeleteErr != nil {
			return errors.Join(ErrConfigStorage, releasesDeleteErr)
		}
		if environmentDeleteErr := tx.Where("namespace_id = ?", namespaceID).Delete(&ConfigEnvironment{}, id).Error; environmentDeleteErr != nil {
			return errors.Join(ErrConfigStorage, environmentDeleteErr)
		}
		return nil
	})
	if err != nil {
		return errors.Join(ErrConfigStorage, err)
	}
	return nil
}

// SaveConfigDraft exposes the package's save config draft value.
//
//nolint:cyclop,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func SaveConfigDraft(ctx context.Context, namespaceID, environmentID uint, raw, rawDescriptions []byte) (*ResolvedConfig, error) {
	config, err := DecodeConfig(raw)
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	if len(encoded) > MaxConfigBytes {
		return nil, errors.Join(ErrConfigInvalid, errConfigurationTooLarge)
	}
	descriptions, err := DecodeConfigDescriptions(rawDescriptions, config)
	if err != nil {
		return nil, err
	}
	encodedDescriptions, err := json.Marshal(descriptions)
	if err != nil {
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	result := &ResolvedConfig{}
	err = DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var environment ConfigEnvironment
		if lockErr := tx.Clauses(clause.Locking{Strength: configLockUpdate}).
			Where("namespace_id = ?", namespaceID).
			First(&environment, environmentID).Error; lockErr != nil {
			if errors.Is(lockErr, gorm.ErrRecordNotFound) {
				return errors.Join(ErrConfigEnvironmentNotFound, lockErr)
			}
			return errors.Join(ErrConfigStorage, lockErr)
		}
		storedConfig, payloadErr := encryptConfigPayload(encoded, draftPayloadContext(namespaceID, environmentID, payloadDraftConfig))
		if payloadErr != nil {
			return payloadErr
		}
		storedDescriptions, payloadErr := encryptConfigPayload(encodedDescriptions, draftPayloadContext(namespaceID, environmentID, payloadDraftDescriptions))
		if payloadErr != nil {
			return payloadErr
		}
		changes := map[string]any{
			columnDraftConfig:       storedConfig,
			columnDraftDescriptions: storedDescriptions,
		}
		if updateErr := tx.Model(&environment).Updates(changes).Error; updateErr != nil {
			return errors.Join(ErrConfigStorage, updateErr)
		}
		resolved, payloadErr := resolveConfig(ctx, tx, namespaceID, environmentID)
		if payloadErr != nil {
			return payloadErr
		}
		*result = *resolved
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return result, nil
}

// GetConfigDraft returns config draft.
func GetConfigDraft(ctx context.Context, namespaceID, environmentID uint) (*ResolvedConfig, map[string]any, ConfigDescriptions, error) {
	resolved, err := resolveConfig(ctx, DB(), namespaceID, environmentID)
	if err != nil {
		return nil, nil, nil, err
	}
	rawDraft, err := decryptConfigPayload(resolved.Environment.DraftConfig, draftPayloadContext(namespaceID, environmentID, payloadDraftConfig))
	if err != nil {
		return nil, nil, nil, err
	}
	draft, err := DecodeConfig(rawDraft)
	if err != nil {
		return nil, nil, nil, errors.Join(ErrConfigStorage, err)
	}
	rawDescriptions, err := decryptConfigPayload(resolved.Environment.DraftDescriptions, draftPayloadContext(namespaceID, environmentID, payloadDraftDescriptions))
	if err != nil {
		return nil, nil, nil, err
	}
	descriptions, err := DecodeConfigDescriptions(rawDescriptions, draft)
	if err != nil {
		return nil, nil, nil, errors.Join(ErrConfigStorage, err)
	}
	return resolved, draft, descriptions, nil
}

// ResolveConfigDraft performs the resolve config draft operation.
func ResolveConfigDraft(ctx context.Context, namespaceID, environmentID uint) (*ResolvedConfig, error) {
	return resolveConfig(ctx, DB(), namespaceID, environmentID)
}

func resolveConfig(ctx context.Context, conn *gorm.DB, namespaceID, environmentID uint) (*ResolvedConfig, error) {
	environments := make([]ConfigEnvironment, 0)
	if err := conn.WithContext(ctx).Where("namespace_id = ?", namespaceID).Order("id ASC").Find(&environments).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return resolveConfigFromEnvironments(environments, environmentID)
}

//nolint:cyclop,funlen,gocognit // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func resolveConfigFromEnvironments(environments []ConfigEnvironment, environmentID uint) (*ResolvedConfig, error) {
	byID := make(map[uint]ConfigEnvironment, len(environments))
	for _, environment := range environments {
		byID[environment.ID] = environment
	}
	target, exists := byID[environmentID]
	if !exists {
		return nil, ErrConfigEnvironmentNotFound
	}

	chain := make([]ConfigEnvironment, 0, maxEnvironmentDepth)
	visited := make(map[uint]struct{}, maxEnvironmentDepth)
	current := target
	for {
		if _, duplicate := visited[current.ID]; duplicate {
			return nil, errors.Join(ErrConfigEnvironmentInvalid, errInheritanceCycle)
		}
		visited[current.ID] = struct{}{}
		chain = append(chain, current)
		if len(chain) > maxEnvironmentDepth {
			return nil, errors.Join(ErrConfigEnvironmentInvalid, errInheritanceTooDeep)
		}
		if current.ParentID == 0 {
			break
		}
		parent, found := byID[current.ParentID]
		if !found {
			return nil, errors.Join(ErrConfigEnvironmentInvalid, errParentEnvironmentMissing)
		}
		current = parent
	}
	slices.Reverse(chain)

	finalConfig := make(map[string]any)
	finalDescriptions := make(ConfigDescriptions)
	for _, environment := range chain {
		rawDraft, err := decryptConfigPayload(environment.DraftConfig, draftPayloadContext(environment.NamespaceID, environment.ID, payloadDraftConfig))
		if err != nil {
			return nil, err
		}
		draft, err := DecodeConfig(rawDraft)
		if err != nil {
			return nil, errors.Join(ErrConfigStorage, err)
		}
		rawDescriptions, err := decryptConfigPayload(environment.DraftDescriptions, draftPayloadContext(environment.NamespaceID, environment.ID, payloadDraftDescriptions))
		if err != nil {
			return nil, err
		}
		descriptions, err := DecodeConfigDescriptions(rawDescriptions, draft)
		if err != nil {
			return nil, errors.Join(ErrConfigStorage, err)
		}
		merged, mergedDescriptions := mergeConfigWithDescriptions(
			finalConfig,
			finalDescriptions,
			draft,
			descriptions,
			"",
		)
		mergedConfig, ok := merged.(map[string]any)
		if !ok {
			return nil, ErrConfigStorage
		}
		finalConfig = mergedConfig
		finalDescriptions = mergedDescriptions
	}
	return &ResolvedConfig{
		Environment:  target,
		Chain:        chain,
		Config:       finalConfig,
		Descriptions: finalDescriptions,
	}, nil
}

// PublishConfigs exposes the package's publish configs value.
//
//nolint:cyclop,funlen,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func PublishConfigs(ctx context.Context, namespaceID uint, input ConfigPublishInput) (*ConfigPublishResult, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if err := validateConfigPublishInput(input); err != nil {
		return nil, err
	}
	ids := uniqueSortedIDs(input.EnvironmentIDs)
	if len(ids) == 0 || len(ids) > maxConfigEnvironments {
		return nil, ErrConfigEnvironmentInvalid
	}
	batchID, err := newConfigBatchID()
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	result := &ConfigPublishResult{
		BatchID:  batchID,
		Reason:   input.Reason,
		Releases: make([]PublishedConfig, 0, len(ids)),
	}
	err = DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		environments, lockErr := lockedConfigEnvironments(ctx, tx, namespaceID)
		if lockErr != nil {
			return lockErr
		}
		for _, id := range ids {
			if !configEnvironmentExists(environments, id) {
				return ErrConfigEnvironmentNotFound
			}
		}
		for _, id := range ids {
			resolved, publishErr := resolveConfigFromEnvironments(environments, id)
			if publishErr != nil {
				return publishErr
			}
			encoded, encodedDescriptions, publishErr := encodeResolvedConfig(resolved)
			if publishErr != nil {
				return publishErr
			}
			var latest uint64
			if versionErr := tx.Model(&ConfigRelease{}).
				Where("environment_id = ?", id).
				Select("COALESCE(MAX(version), 0)").
				Scan(&latest).Error; versionErr != nil {
				return errors.Join(ErrConfigStorage, versionErr)
			}
			release := &ConfigRelease{
				EnvironmentID: id,
				BatchID:       batchID,
				Reason:        input.Reason,
				Version:       latest + 1,
			}
			storedConfig, publishErr := encryptConfigPayload([]byte(encoded), releasePayloadContext(namespaceID, id, release.Version, payloadReleaseConfig))
			if publishErr != nil {
				return publishErr
			}
			storedDescriptions, publishErr := encryptConfigPayload([]byte(encodedDescriptions), releasePayloadContext(namespaceID, id, release.Version, payloadReleaseDescriptions))
			if publishErr != nil {
				return publishErr
			}
			release.Config = storedConfig
			release.Descriptions = storedDescriptions
			if createErr := tx.Create(release).Error; createErr != nil {
				return errors.Join(ErrConfigStorage, createErr)
			}
			result.Releases = append(result.Releases, PublishedConfig{
				EnvironmentID: id,
				BatchID:       batchID,
				Reason:        release.Reason,
				Version:       release.Version,
				Config:        resolved.Config,
				Descriptions:  resolved.Descriptions,
				PublishedAt:   release.CreatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return result, nil
}

func validateConfigPublishInput(input ConfigPublishInput) error {
	if strings.TrimSpace(input.Reason) == "" {
		return errors.Join(ErrConfigReleaseInvalid, errConfigReleaseReasonRequired)
	}
	if utf8.RuneCountInString(input.Reason) > maxConfigReleaseReasonLength {
		return errors.Join(ErrConfigReleaseInvalid, errConfigReleaseReasonTooLong)
	}
	return nil
}

func encodeResolvedConfig(resolved *ResolvedConfig) (string, string, error) {
	encoded, err := json.Marshal(resolved.Config)
	if err != nil {
		return "", "", errors.Join(ErrConfigInvalid, err)
	}
	encodedDescriptions, err := json.Marshal(resolved.Descriptions)
	if err != nil {
		return "", "", errors.Join(ErrConfigInvalid, err)
	}
	return string(encoded), string(encodedDescriptions), nil
}

// LatestPublishedConfig performs the latest published config operation.
func LatestPublishedConfig(ctx context.Context, namespaceID, environmentID uint) (*PublishedConfig, error) {
	if err := requireConfigEnvironment(ctx, DB(), namespaceID, environmentID); err != nil {
		return nil, err
	}
	var release ConfigRelease
	err := DB().WithContext(ctx).
		Where("environment_id = ?", environmentID).
		Order("version DESC").
		First(&release).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.Join(ErrConfigReleaseNotFound, err)
		}
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return publishedConfigFromRelease(namespaceID, release)
}

// LatestPublishedConfigBySlugs resolves stable public identifiers before
// returning the latest immutable release for an environment.
func LatestPublishedConfigBySlugs(
	ctx context.Context,
	namespaceSlug string,
	environmentSlug string,
	apiKey string,
) (*ConfigNamespace, *ConfigEnvironment, *PublishedConfig, error) {
	return latestPublishedConfigBySlugs(ctx, namespaceSlug, environmentSlug, func(namespace *ConfigNamespace) error {
		return authenticateNamespaceAPIKey(namespace, apiKey)
	})
}

// LatestPublishedConfigBySlugsInternal is for trusted in-process consumers
// that already have direct database access. External callers must use the
// runtime HTTP API and authenticate with the namespace API Key.
func LatestPublishedConfigBySlugsInternal(
	ctx context.Context,
	namespaceSlug string,
	environmentSlug string,
) (*ConfigNamespace, *ConfigEnvironment, *PublishedConfig, error) {
	return latestPublishedConfigBySlugs(ctx, namespaceSlug, environmentSlug, nil)
}

//nolint:cyclop,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func latestPublishedConfigBySlugs(
	ctx context.Context,
	namespaceSlug string,
	environmentSlug string,
	authenticate func(*ConfigNamespace) error,
) (*ConfigNamespace, *ConfigEnvironment, *PublishedConfig, error) {
	namespaceSlug = strings.ToLower(strings.TrimSpace(namespaceSlug))
	environmentSlug = strings.ToLower(strings.TrimSpace(environmentSlug))
	if !configSlugPattern.MatchString(namespaceSlug) {
		return nil, nil, nil, ErrConfigNamespaceInvalid
	}
	if !configSlugPattern.MatchString(environmentSlug) {
		return nil, nil, nil, ErrConfigEnvironmentInvalid
	}

	namespace := &ConfigNamespace{}
	if err := DB().WithContext(ctx).Where("slug = ?", namespaceSlug).First(namespace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if authenticate != nil {
				return nil, nil, nil, errors.Join(ErrConfigAPIKeyUnauthorized, err)
			}
			return nil, nil, nil, errors.Join(ErrConfigNamespaceNotFound, err)
		}
		return nil, nil, nil, errors.Join(ErrConfigStorage, err)
	}
	if authenticate != nil {
		if err := authenticate(namespace); err != nil {
			return nil, nil, nil, err
		}
	}
	environment := &ConfigEnvironment{}
	if err := DB().WithContext(ctx).
		Where("namespace_id = ? AND slug = ?", namespace.ID, environmentSlug).
		First(environment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, errors.Join(ErrConfigEnvironmentNotFound, err)
		}
		return nil, nil, nil, errors.Join(ErrConfigStorage, err)
	}
	published, err := LatestPublishedConfig(ctx, namespace.ID, environment.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return namespace, environment, published, nil
}

// PublishedConfigVersion performs the published config version operation.
func PublishedConfigVersion(ctx context.Context, namespaceID, environmentID uint, version uint64) (*PublishedConfig, error) {
	if environmentID == 0 || version == 0 {
		return nil, ErrConfigReleaseNotFound
	}
	if err := requireConfigEnvironment(ctx, DB(), namespaceID, environmentID); err != nil {
		return nil, err
	}
	var release ConfigRelease
	err := DB().WithContext(ctx).
		Where("environment_id = ? AND version = ?", environmentID, version).
		First(&release).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.Join(ErrConfigReleaseNotFound, err)
		}
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return publishedConfigFromRelease(namespaceID, release)
}

// ListConfigReleases lists config releases.
func ListConfigReleases(ctx context.Context, namespaceID, environmentID uint) ([]ConfigReleaseSummary, error) {
	if environmentID == 0 {
		return nil, ErrConfigEnvironmentNotFound
	}
	var count int64
	if err := DB().WithContext(ctx).Model(&ConfigEnvironment{}).Where("namespace_id = ? AND id = ?", namespaceID, environmentID).Count(&count).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	if count == 0 {
		return nil, ErrConfigEnvironmentNotFound
	}
	releases := make([]ConfigReleaseSummary, 0)
	if err := DB().WithContext(ctx).
		Model(&ConfigRelease{}).
		Select("environment_id, batch_id, reason, version, created_at AS published_at").
		Where("environment_id = ?", environmentID).
		Order("version DESC").
		Scan(&releases).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return releases, nil
}

// ListAllConfigReleases lists all config releases.
func ListAllConfigReleases(ctx context.Context, namespaceID uint) ([]ConfigReleaseSummary, error) {
	if err := requireConfigNamespace(ctx, DB(), namespaceID); err != nil {
		return nil, err
	}
	releases := make([]ConfigReleaseSummary, 0)
	if err := DB().WithContext(ctx).
		Model(&ConfigRelease{}).
		Select("environment_id, batch_id, reason, version, created_at AS published_at").
		Where("environment_id IN (?)", DB().Model(&ConfigEnvironment{}).Select("id").Where("namespace_id = ?", namespaceID)).
		Order("environment_id ASC, version DESC").
		Scan(&releases).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return releases, nil
}

func publishedConfigFromRelease(namespaceID uint, release ConfigRelease) (*PublishedConfig, error) {
	rawConfig, rawDescriptions, err := decodeStoredRelease(release, namespaceID)
	if err != nil {
		return nil, err
	}
	config, err := DecodeConfig([]byte(rawConfig))
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	descriptions, err := DecodeConfigDescriptions([]byte(rawDescriptions), config)
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return &PublishedConfig{
		EnvironmentID: release.EnvironmentID,
		BatchID:       release.BatchID,
		Reason:        release.Reason,
		Version:       release.Version,
		Config:        config,
		Descriptions:  descriptions,
		PublishedAt:   release.CreatedAt,
	}, nil
}

func decodeStoredRelease(release ConfigRelease, namespaceID uint) (string, string, error) {
	rawConfig, err := decryptConfigPayload(
		release.Config,
		releasePayloadContext(namespaceID, release.EnvironmentID, release.Version, payloadReleaseConfig),
	)
	if err != nil {
		return "", "", err
	}
	rawDescriptions, err := decryptConfigPayload(
		release.Descriptions,
		releasePayloadContext(namespaceID, release.EnvironmentID, release.Version, payloadReleaseDescriptions),
	)
	if err != nil {
		return "", "", err
	}
	return string(rawConfig), string(rawDescriptions), nil
}

func encryptConfigPayload(plaintext []byte, encryptionContext string) (string, error) {
	stored, err := configcrypt.Encrypt(plaintext, encryptionContext)
	if err != nil {
		return "", errors.Join(ErrConfigStorage, err)
	}
	return stored, nil
}

func decryptConfigPayload(stored, encryptionContext string) ([]byte, error) {
	plaintext, _, err := configcrypt.Decrypt(stored, encryptionContext)
	if err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return plaintext, nil
}

func encryptNamespaceAPIKey(apiKey string, namespaceID uint) (string, error) {
	if !configcrypt.Enabled() {
		log.Error().Uint("namespace_id", namespaceID).Msg("namespace API key encryption requires an enabled keyring")
		return "", errors.Join(ErrConfigEncryptionRequired, configcrypt.ErrDisabled)
	}
	return encryptConfigPayload([]byte(apiKey), namespaceAPIKeyContext(namespaceID))
}

func authenticateNamespaceAPIKey(namespace *ConfigNamespace, provided string) error {
	if provided == "" || namespace.APIKey == "" {
		return ErrConfigAPIKeyUnauthorized
	}
	stored, err := decryptConfigPayload(namespace.APIKey, namespaceAPIKeyContext(namespace.ID))
	if err != nil {
		return err
	}
	storedDigest := sha256.Sum256(stored)
	providedDigest := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(storedDigest[:], providedDigest[:]) != 1 {
		return ErrConfigAPIKeyUnauthorized
	}
	return nil
}

func namespaceAPIKeyContext(namespaceID uint) string {
	return configPayloadContext(namespaceID, 0, 0, payloadNamespaceAPIKey)
}

func draftPayloadContext(namespaceID, environmentID uint, kind string) string {
	return configPayloadContext(namespaceID, environmentID, 0, kind)
}

func releasePayloadContext(namespaceID, environmentID uint, version uint64, kind string) string {
	return configPayloadContext(namespaceID, environmentID, version, kind)
}

func configPayloadContext(namespaceID, environmentID uint, version uint64, kind string) string {
	return "namespace=" + strconv.FormatUint(uint64(namespaceID), 10) +
		"|environment=" + strconv.FormatUint(uint64(environmentID), 10) +
		"|version=" + strconv.FormatUint(version, 10) +
		"|kind=" + kind
}

// ReencryptConfigStorage encrypts legacy plaintext records and rewraps data
// keys that reference an inactive KEK. It is safe to rerun after interruption.
func ReencryptConfigStorage(ctx context.Context) (*ConfigReencryptResult, error) {
	if !configcrypt.Enabled() {
		return nil, errors.Join(ErrConfigStorage, configcrypt.ErrDisabled)
	}
	result := &ConfigReencryptResult{}
	if err := reencryptConfigNamespaces(ctx, result); err != nil {
		return nil, err
	}
	if err := reencryptConfigEnvironments(ctx, result); err != nil {
		return nil, err
	}
	if err := reencryptConfigReleases(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func reencryptConfigNamespaces(ctx context.Context, result *ConfigReencryptResult) error {
	var lastID uint
	for {
		batchSize := 0
		err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			namespaces := make([]ConfigNamespace, 0, configReencryptBatchSize)
			if err := tx.Clauses(clause.Locking{Strength: configLockUpdate}).
				Where("id > ?", lastID).
				Order("id ASC").
				Limit(configReencryptBatchSize).
				Find(&namespaces).Error; err != nil {
				return errors.Join(ErrConfigStorage, err)
			}
			batchSize = len(namespaces)
			for index := range namespaces {
				namespace := &namespaces[index]
				lastID = namespace.ID
				if namespace.APIKey == "" {
					continue
				}
				updated, changed, err := configcrypt.Reencrypt(namespace.APIKey, namespaceAPIKeyContext(namespace.ID))
				if err != nil {
					return errors.Join(ErrConfigStorage, err)
				}
				if !changed {
					continue
				}
				if updateErr := tx.Model(namespace).UpdateColumn("api_key", updated).Error; updateErr != nil {
					return errors.Join(ErrConfigStorage, updateErr)
				}
				result.NamespaceRecords++
				result.Payloads++
			}
			return nil
		})
		if err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if batchSize < configReencryptBatchSize {
			return nil
		}
	}
}

//nolint:gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func reencryptConfigEnvironments(ctx context.Context, result *ConfigReencryptResult) error {
	var lastID uint
	for {
		batchSize := 0
		err := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			environments := make([]ConfigEnvironment, 0, configReencryptBatchSize)
			if err := tx.Clauses(clause.Locking{Strength: configLockUpdate}).
				Where("id > ?", lastID).
				Order("id ASC").
				Limit(configReencryptBatchSize).
				Find(&environments).Error; err != nil {
				return errors.Join(ErrConfigStorage, err)
			}
			batchSize = len(environments)
			for index := range environments {
				environment := &environments[index]
				updatedConfig, updatedDescriptions, payloads, changed, err := reencryptPayloadPair(
					environment.DraftConfig,
					environment.DraftDescriptions,
					draftPayloadContext(environment.NamespaceID, environment.ID, payloadDraftConfig),
					draftPayloadContext(environment.NamespaceID, environment.ID, payloadDraftDescriptions),
				)
				if err != nil {
					return err
				}
				if changed {
					if updateErr := tx.Model(environment).UpdateColumns(map[string]any{
						columnDraftConfig:       updatedConfig,
						columnDraftDescriptions: updatedDescriptions,
					}).Error; updateErr != nil {
						return errors.Join(ErrConfigStorage, updateErr)
					}
					result.EnvironmentRecords++
					result.Payloads += payloads
				}
				lastID = environment.ID
			}
			return nil
		})
		if err != nil {
			return errors.Join(ErrConfigStorage, err)
		}
		if batchSize < configReencryptBatchSize {
			return nil
		}
	}
}

//nolint:cyclop,gocognit // This bounded transactional workflow keeps lock ordering and classified error exits together.
func reencryptConfigReleases(ctx context.Context, result *ConfigReencryptResult) error {
	namespaceByEnvironment, err := configEnvironmentNamespaces(ctx)
	if err != nil {
		return err
	}
	var lastID uint
	for {
		batchSize := 0
		batchErr := DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			releases := make([]ConfigRelease, 0, configReencryptBatchSize)
			if queryErr := tx.Clauses(clause.Locking{Strength: configLockUpdate}).
				Where("id > ?", lastID).
				Order("id ASC").
				Limit(configReencryptBatchSize).
				Find(&releases).Error; queryErr != nil {
				return errors.Join(ErrConfigStorage, queryErr)
			}
			batchSize = len(releases)
			for index := range releases {
				release := &releases[index]
				namespaceID, exists := namespaceByEnvironment[release.EnvironmentID]
				if !exists {
					return ErrConfigEnvironmentNotFound
				}
				updatedConfig, updatedDescriptions, payloads, changed, reencryptErr := reencryptPayloadPair(
					release.Config,
					release.Descriptions,
					releasePayloadContext(namespaceID, release.EnvironmentID, release.Version, payloadReleaseConfig),
					releasePayloadContext(namespaceID, release.EnvironmentID, release.Version, payloadReleaseDescriptions),
				)
				if reencryptErr != nil {
					return reencryptErr
				}
				if changed {
					if updateErr := tx.Model(release).UpdateColumns(map[string]any{
						"config":       updatedConfig,
						"descriptions": updatedDescriptions,
					}).Error; updateErr != nil {
						return errors.Join(ErrConfigStorage, updateErr)
					}
					result.ReleaseRecords++
					result.Payloads += payloads
				}
				lastID = release.ID
			}
			return nil
		})
		if batchErr != nil {
			return errors.Join(ErrConfigStorage, batchErr)
		}
		if batchSize < configReencryptBatchSize {
			return nil
		}
	}
}

func configEnvironmentNamespaces(ctx context.Context) (map[uint]uint, error) {
	rows := make([]struct {
		ID          uint
		NamespaceID uint
	}, 0)
	if err := DB().WithContext(ctx).Model(&ConfigEnvironment{}).Select("id, namespace_id").Scan(&rows).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	namespaceByEnvironment := make(map[uint]uint, len(rows))
	for _, row := range rows {
		namespaceByEnvironment[row.ID] = row.NamespaceID
	}
	return namespaceByEnvironment, nil
}

func reencryptPayloadPair(config, descriptions, configContext, descriptionsContext string) (string, string, int64, bool, error) {
	updatedConfig, configChanged, err := configcrypt.Reencrypt(config, configContext)
	if err != nil {
		return "", "", 0, false, errors.Join(ErrConfigStorage, err)
	}
	updatedDescriptions, descriptionsChanged, err := configcrypt.Reencrypt(descriptions, descriptionsContext)
	if err != nil {
		return "", "", 0, false, errors.Join(ErrConfigStorage, err)
	}
	var payloads int64
	if configChanged {
		payloads++
	}
	if descriptionsChanged {
		payloads++
	}
	return updatedConfig, updatedDescriptions, payloads, configChanged || descriptionsChanged, nil
}

// DecodeConfig decodes config.
func DecodeConfig(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > MaxConfigBytes {
		return nil, errors.Join(ErrConfigInvalid, errConfigSizeInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	if config == nil {
		return nil, errors.Join(ErrConfigInvalid, errConfigRootNotObject)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errConfigMultipleJSONValues
		}
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	nodes := 0
	if err := validateConfigValue(config, 1, &nodes); err != nil {
		return nil, err
	}
	return config, nil
}

// DecodeConfigDescriptions exposes the package's decode config descriptions value.
//
//nolint:cyclop,gocognit // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func DecodeConfigDescriptions(raw []byte, config map[string]any) (ConfigDescriptions, error) {
	if len(raw) == 0 {
		return make(ConfigDescriptions), nil
	}
	if len(raw) > MaxConfigDescriptionBytes {
		return nil, errors.Join(ErrConfigInvalid, errDescriptionsTooLarge)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var descriptions ConfigDescriptions
	if err := decoder.Decode(&descriptions); err != nil {
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	if descriptions == nil {
		descriptions = make(ConfigDescriptions)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errDescriptionsMultipleValues
		}
		return nil, errors.Join(ErrConfigInvalid, err)
	}
	if len(descriptions) > maxConfigNodes {
		return nil, errors.Join(ErrConfigInvalid, errTooManyDescriptions)
	}
	normalized := make(ConfigDescriptions, len(descriptions))
	for pointer, description := range descriptions {
		description = strings.TrimSpace(description)
		if description == "" {
			continue
		}
		if len(description) > maxConfigDescriptionLength {
			return nil, errors.Join(ErrConfigInvalid, errDescriptionTooLong)
		}
		path, err := parseJSONPointer(pointer)
		if err != nil || !configPathExists(config, path) {
			return nil, errors.Join(ErrConfigInvalid, errDescriptionPathInvalid)
		}
		normalized[pointer] = description
	}
	return normalized, nil
}

//nolint:cyclop,gocognit // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func validateConfigValue(value any, depth int, nodes *int) error {
	(*nodes)++
	if depth > maxConfigDepth {
		return errors.Join(ErrConfigInvalid, errConfigNestingTooDeep)
	}
	if *nodes > maxConfigNodes {
		return errors.Join(ErrConfigInvalid, errTooManyConfigValues)
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > maxConfigCollection {
			return errors.Join(ErrConfigInvalid, errTooManyObjectFields)
		}
		for key, child := range typed {
			if strings.TrimSpace(key) == "" || len(key) > maxConfigKeyLength {
				return errors.Join(ErrConfigInvalid, errConfigKeyInvalid)
			}
			if err := validateConfigValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > maxConfigCollection {
			return errors.Join(ErrConfigInvalid, errTooManyArrayItems)
		}
		for _, child := range typed {
			if err := validateConfigValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > maxConfigStringLength {
			return errors.Join(ErrConfigInvalid, errConfigStringTooLong)
		}
	case bool, json.Number:
		return nil
	default:
		return errors.Join(ErrConfigInvalid, errUnsupportedConfigValue)
	}
	return nil
}

func mergeConfig(base, override any) any {
	baseObject, baseIsObject := base.(map[string]any)
	overrideObject, overrideIsObject := override.(map[string]any)
	if !baseIsObject || !overrideIsObject {
		return cloneConfigValue(override)
	}
	merged := make(map[string]any, len(baseObject)+len(overrideObject))
	for key, value := range baseObject {
		merged[key] = cloneConfigValue(value)
	}
	for key, value := range overrideObject {
		if inherited, exists := merged[key]; exists {
			merged[key] = mergeConfig(inherited, value)
		} else {
			merged[key] = cloneConfigValue(value)
		}
	}
	return merged
}

func mergeConfigWithDescriptions(
	base any,
	baseDescriptions ConfigDescriptions,
	override any,
	overrideDescriptions ConfigDescriptions,
	pointer string,
) (any, ConfigDescriptions) {
	mergedDescriptions := cloneConfigDescriptions(baseDescriptions)
	baseObject, baseIsObject := base.(map[string]any)
	overrideObject, overrideIsObject := override.(map[string]any)
	if !baseIsObject || !overrideIsObject {
		removeDescriptionSubtree(mergedDescriptions, pointer)
		copyDescriptionSubtree(mergedDescriptions, overrideDescriptions, pointer)
		return cloneConfigValue(override), mergedDescriptions
	}

	merged := make(map[string]any, len(baseObject)+len(overrideObject))
	for key, value := range baseObject {
		merged[key] = cloneConfigValue(value)
	}
	if description, exists := overrideDescriptions[pointer]; exists && pointer != "" {
		mergedDescriptions[pointer] = description
	}
	for key, value := range overrideObject {
		childPointer := appendJSONPointer(pointer, key)
		if inherited, exists := merged[key]; exists {
			merged[key], mergedDescriptions = mergeConfigWithDescriptions(
				inherited,
				mergedDescriptions,
				value,
				overrideDescriptions,
				childPointer,
			)
			continue
		}
		merged[key] = cloneConfigValue(value)
		copyDescriptionSubtree(mergedDescriptions, overrideDescriptions, childPointer)
	}
	return merged, mergedDescriptions
}

//nolint:gocognit // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func parseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, errJSONPointerItemRequired
	}
	encodedSegments := strings.Split(pointer[1:], "/")
	segments := make([]string, len(encodedSegments))
	for index, encoded := range encodedSegments {
		decoded := make([]byte, 0, len(encoded))
		for position := 0; position < len(encoded); position++ {
			if encoded[position] != '~' {
				decoded = append(decoded, encoded[position])
				continue
			}
			if position+1 >= len(encoded) || (encoded[position+1] != '0' && encoded[position+1] != '1') {
				return nil, errJSONPointerInvalidEscape
			}
			position++
			if encoded[position] == '0' {
				decoded = append(decoded, '~')
			} else {
				decoded = append(decoded, '/')
			}
		}
		segments[index] = string(decoded)
	}
	return segments, nil
}

func configPathExists(config map[string]any, path []string) bool {
	var current any = config
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = typed[segment]
			if !exists {
				return false
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) || strconv.Itoa(index) != segment {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	return true
}

func appendJSONPointer(pointer, segment string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
	return pointer + "/" + escaped
}

func cloneConfigDescriptions(descriptions ConfigDescriptions) ConfigDescriptions {
	cloned := make(ConfigDescriptions, len(descriptions))
	maps.Copy(cloned, descriptions)
	return cloned
}

func removeDescriptionSubtree(descriptions ConfigDescriptions, pointer string) {
	if pointer == "" {
		clear(descriptions)
		return
	}
	for candidate := range descriptions {
		if candidate == pointer || strings.HasPrefix(candidate, pointer+"/") {
			delete(descriptions, candidate)
		}
	}
}

func copyDescriptionSubtree(target, source ConfigDescriptions, pointer string) {
	for candidate, description := range source {
		if pointer == "" || candidate == pointer || strings.HasPrefix(candidate, pointer+"/") {
			target[candidate] = description
		}
	}
}

func cloneConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = cloneConfigValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			cloned[index] = cloneConfigValue(child)
		}
		return cloned
	default:
		return typed
	}
}

func normalizeConfigEnvironmentInput(input ConfigEnvironmentInput) ConfigEnvironmentInput {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description = strings.TrimSpace(input.Description)
	return input
}

func normalizeConfigNamespaceInput(input ConfigNamespaceInput) ConfigNamespaceInput {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description = strings.TrimSpace(input.Description)
	return input
}

//nolint:cyclop // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func validateConfigNamespaceInput(input ConfigNamespaceInput, requireAPIKey bool) error {
	if input.Name == "" || len(input.Name) > 100 {
		return errors.Join(ErrConfigNamespaceInvalid, errNamespaceNameInvalid)
	}
	if !configSlugPattern.MatchString(input.Slug) {
		return errors.Join(ErrConfigNamespaceInvalid, errNamespaceSlugInvalid)
	}
	if len(input.Description) > 500 {
		return errors.Join(ErrConfigNamespaceInvalid, errNamespaceDescriptionLong)
	}
	if requireAPIKey && input.APIKey == "" {
		return errors.Join(ErrConfigAPIKeyInvalid, errNamespaceAPIKeyRequired)
	}
	if input.APIKey != "" && (len(input.APIKey) < minConfigAPIKeyLength || len(input.APIKey) > maxConfigAPIKeyLength ||
		!configAPIKeyPattern.MatchString(input.APIKey)) {
		return errors.Join(ErrConfigAPIKeyInvalid, errNamespaceAPIKeyLength)
	}
	return nil
}

func validateConfigEnvironmentInput(input ConfigEnvironmentInput) error {
	if input.Name == "" || len(input.Name) > 100 {
		return errors.Join(ErrConfigEnvironmentInvalid, errEnvironmentNameInvalid)
	}
	if !configSlugPattern.MatchString(input.Slug) {
		return errors.Join(ErrConfigEnvironmentInvalid, errEnvironmentSlugInvalid)
	}
	if len(input.Description) > 500 {
		return errors.Join(ErrConfigEnvironmentInvalid, errEnvironmentDescriptionLong)
	}
	return nil
}

func lockedConfigEnvironments(ctx context.Context, tx *gorm.DB, namespaceID uint) ([]ConfigEnvironment, error) {
	if err := requireConfigNamespace(ctx, tx, namespaceID); err != nil {
		return nil, err
	}
	environments := make([]ConfigEnvironment, 0)
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: configLockUpdate}).
		Where("namespace_id = ?", namespaceID).
		Order("id ASC").
		Find(&environments).Error; err != nil {
		return nil, errors.Join(ErrConfigStorage, err)
	}
	return environments, nil
}

func requireConfigNamespace(ctx context.Context, conn *gorm.DB, namespaceID uint) error {
	if namespaceID == 0 {
		return ErrConfigNamespaceNotFound
	}
	var count int64
	if err := conn.WithContext(ctx).Model(&ConfigNamespace{}).Where("id = ?", namespaceID).Count(&count).Error; err != nil {
		return errors.Join(ErrConfigStorage, err)
	}
	if count == 0 {
		return ErrConfigNamespaceNotFound
	}
	return nil
}

func requireConfigEnvironment(ctx context.Context, conn *gorm.DB, namespaceID, environmentID uint) error {
	if namespaceID == 0 || environmentID == 0 {
		return ErrConfigEnvironmentNotFound
	}
	var count int64
	if err := conn.WithContext(ctx).
		Model(&ConfigEnvironment{}).
		Where("namespace_id = ? AND id = ?", namespaceID, environmentID).
		Count(&count).Error; err != nil {
		return errors.Join(ErrConfigStorage, err)
	}
	if count == 0 {
		return ErrConfigEnvironmentNotFound
	}
	return nil
}

//nolint:gocognit // This bounded recursive validation keeps depth, cardinality, and type invariants explicit.
func validateEnvironmentParent(environments []ConfigEnvironment, environmentID, parentID uint) error {
	if parentID == 0 {
		return nil
	}
	if parentID == environmentID {
		return errors.Join(ErrConfigEnvironmentInvalid, errSelfInheritance)
	}
	byID := make(map[uint]ConfigEnvironment, len(environments))
	for _, environment := range environments {
		byID[environment.ID] = environment
	}
	parent, exists := byID[parentID]
	if !exists {
		return errors.Join(ErrConfigEnvironmentInvalid, errParentEnvironmentMissing)
	}
	visited := map[uint]struct{}{environmentID: {}}
	for depth := 1; ; depth++ {
		if depth > maxEnvironmentDepth {
			return errors.Join(ErrConfigEnvironmentInvalid, errInheritanceTooDeep)
		}
		if _, duplicate := visited[parent.ID]; duplicate {
			return errors.Join(ErrConfigEnvironmentInvalid, errInheritanceCycle)
		}
		visited[parent.ID] = struct{}{}
		if parent.ParentID == 0 {
			return nil
		}
		parent, exists = byID[parent.ParentID]
		if !exists {
			return errors.Join(ErrConfigEnvironmentInvalid, errParentEnvironmentMissing)
		}
	}
}

func configEnvironmentExists(environments []ConfigEnvironment, id uint) bool {
	for _, environment := range environments {
		if environment.ID == id {
			return true
		}
	}
	return false
}

func environmentSlugExists(environments []ConfigEnvironment, slug string, exceptID uint) bool {
	for _, environment := range environments {
		if environment.ID != exceptID && environment.Slug == slug {
			return true
		}
	}
	return false
}

func uniqueSortedIDs(ids []uint) []uint {
	unique := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			unique[id] = struct{}{}
		}
	}
	result := make([]uint, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	slices.Sort(result)
	return result
}

func newConfigBatchID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.Join(ErrConfigStorage, err)
	}
	return hex.EncodeToString(random), nil
}
