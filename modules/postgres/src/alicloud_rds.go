package main

import (
	"errors"
	"os"
	"strings"

	kusionapiv1 "kusionstack.io/kusion-api-go/api.kusion.io/v1"
	"kusionstack.io/kusion-module-framework/pkg/module"
)

var ErrEmptyAlicloudProviderRegion = errors.New("empty alicloud provider region")

var (
	alicloudRegionEnv    = "ALICLOUD_REGION"
	alicloudDBInstance   = "alicloud_db_instance"
	alicloudDBConnection = "alicloud_db_connection"
	alicloudRDSAccount   = "alicloud_rds_account"
)

var defaultAlicloudProviderCfg = module.ProviderConfig{
	Source:  "aliyun/alicloud",
	Version: "1.209.1",
}

type alicloudServerlessConfig struct {
	AutoPause   bool `yaml:"auto_pause" json:"auto_pause"`
	SwitchForce bool `yaml:"switch_force" json:"switch_force"`
	MaxCapacity int  `yaml:"max_capacity,omitempty" json:"max_capacity,omitempty"`
	MinCapacity int  `yaml:"min_capacity,omitempty" json:"min_capacity,omitempty"`
}

// GenerateAlicloudResources generates Alicloud provided PostgreSQL database instance.
func (postgres *PostgreSQL) GenerateAlicloudResources(request *module.GeneratorRequest) ([]kusionapiv1.Resource, *kusionapiv1.Patcher, error) {
	var resources []kusionapiv1.Resource

	// Set the Alicloud provider with the default provider config.
	alicloudProviderCfg := defaultAlicloudProviderCfg

	// Get the Alicloud Terraform provider region, which should not be empty.
	var region string
	if region = module.TerraformProviderRegion(alicloudProviderCfg); region == "" {
		region = os.Getenv(alicloudRegionEnv)
	}
	if region == "" {
		return nil, nil, ErrEmptyAlicloudProviderRegion
	}

	// Build random_password resource.
	randomPasswordRes, randomPasswordID, err := postgres.GenerateTFRandomPassword(request)
	if err != nil {
		return nil, nil, err
	}
	resources = append(resources, *randomPasswordRes)

	// Build alicloud_db_instance resource.
	alicloudDBInstanceRes, alicloudDBInstanceID, err := postgres.generateAlicloudDBInstance(
		alicloudProviderCfg, region,
	)
	if err != nil {
		return nil, nil, err
	}
	resources = append(resources, *alicloudDBInstanceRes)

	// Build alicloud_db_connection resource.
	var alicloudDBConnectionRes *kusionapiv1.Resource
	var alicloudDBConnectionID string
	if IsPublicAccessible(postgres.SecurityIPs) {
		alicloudDBConnectionRes, alicloudDBConnectionID, err = postgres.generateAlicloudDBConnection(
			alicloudProviderCfg,
			region, alicloudDBInstanceID,
		)
		if err != nil {
			return nil, nil, err
		}

		resources = append(resources, *alicloudDBConnectionRes)
	}

	// Build alicloud_rds_account resuorce.
	alicloudRDSAccountRes, err := postgres.generateAlicloudRDSAccount(
		alicloudProviderCfg,
		region, postgres.Username, randomPasswordID, alicloudDBInstanceID,
	)
	if err != nil {
		return nil, nil, err
	}
	resources = append(resources, *alicloudRDSAccountRes)

	hostAddress := module.KusionPathDependency(alicloudDBInstanceID, "connection_string")
	if !postgres.PrivateRouting {
		// Set the public network connection string as the host address.
		hostAddress = module.KusionPathDependency(alicloudDBConnectionID, "connection_string")
	}
	password := module.KusionPathDependency(randomPasswordID, "result")

	// Build Kubernetes Secret with the hostAddress, username and password of the Alicloud provided PostgreSQL instance,
	// and inject the credentials as the environment variable patcher.
	dbSecret, patcher, err := postgres.GenerateDBSecret(request, hostAddress, postgres.Username, password)
	if err != nil {
		return nil, nil, err
	}
	resources = append(resources, *dbSecret)

	return resources, patcher, nil
}

// generateAlicloudDBInstance generates alicloud_db_instance resource
// for the Alicloud provided PostgreSQL database instance.
func (postgres *PostgreSQL) generateAlicloudDBInstance(alicloudProviderCfg module.ProviderConfig,
	region string,
) (*kusionapiv1.Resource, string, error) {
	resAttrs := map[string]interface{}{
		"category":         postgres.Category,
		"engine":           "PostgreSQL",
		"engine_version":   postgres.Version,
		"instance_storage": postgres.Size,
		"instance_type":    postgres.InstanceType,
		"security_ips":     postgres.SecurityIPs,
		"vswitch_id":       postgres.SubnetID,
		"instance_name":    postgres.DatabaseName,
	}

	// Set the serverless-specific attributes of the alicloud_db_instance resource.
	if strings.Contains(postgres.Category, "serverless") {
		resAttrs["db_instance_storage_type"] = "cloud_essd"
		resAttrs["instance_charge_type"] = "Serverless"

		serverlessConfig := alicloudServerlessConfig{
			MaxCapacity: 8,
			MinCapacity: 1,
		}
		serverlessConfig.AutoPause = false
		serverlessConfig.SwitchForce = false

		resAttrs["serverless_config"] = []alicloudServerlessConfig{
			serverlessConfig,
		}
	}

	id, err := module.TerraformResourceID(alicloudProviderCfg, alicloudDBInstance, postgres.DatabaseName)
	if err != nil {
		return nil, "", err
	}

	alicloudProviderCfg.ProviderMeta = map[string]any{"region": region}
	resource, err := module.WrapTFResourceToKusionResource(alicloudProviderCfg, alicloudDBInstance, id, resAttrs, nil)
	if err != nil {
		return nil, "", err
	}

	return resource, id, nil
}

// generateAlicloudDBConnection generates alicloud_db_connection resource
// for the Alicloud provided PostgreSQL database instance.
func (postgres *PostgreSQL) generateAlicloudDBConnection(alicloudProviderCfg module.ProviderConfig,
	region, dbInstanceID string,
) (*kusionapiv1.Resource, string, error) {
	resAttrs := map[string]interface{}{
		"instance_id": module.KusionPathDependency(dbInstanceID, "id"),
		"port":        5432,
	}

	id, err := module.TerraformResourceID(alicloudProviderCfg, alicloudDBConnection, postgres.DatabaseName)
	if err != nil {
		return nil, "", err
	}

	alicloudProviderCfg.ProviderMeta = map[string]any{"region": region}
	resource, err := module.WrapTFResourceToKusionResource(alicloudProviderCfg, alicloudDBConnection, id, resAttrs, nil)
	if err != nil {
		return nil, "", err
	}

	return resource, id, nil
}

// generateAlicloudRDSAccount generates alicloud_rds_account resource
// for the Alicloud provided PostgreSQL database instance.
func (postgres *PostgreSQL) generateAlicloudRDSAccount(alicloudProviderCfg module.ProviderConfig,
	region, accountName, randomPasswordID, dbInstanceID string,
) (*kusionapiv1.Resource, error) {
	resAttrs := map[string]interface{}{
		"account_name":     accountName,
		"account_password": module.KusionPathDependency(randomPasswordID, "result"),
		"account_type":     "Super",
		"db_instance_id":   module.KusionPathDependency(dbInstanceID, "id"),
	}

	id, err := module.TerraformResourceID(alicloudProviderCfg, alicloudRDSAccount, postgres.DatabaseName)
	if err != nil {
		return nil, err
	}

	alicloudProviderCfg.ProviderMeta = map[string]any{"region": region}
	resource, err := module.WrapTFResourceToKusionResource(alicloudProviderCfg, alicloudRDSAccount, id, resAttrs, nil)
	if err != nil {
		return nil, err
	}

	return resource, nil
}
