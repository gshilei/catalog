package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"gopkg.in/yaml.v2"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
	kusionapiv1 "kusionstack.io/kusion-api-go/api.kusion.io/v1"
	"kusionstack.io/kusion-module-framework/pkg/log"
	"kusionstack.io/kusion-module-framework/pkg/module"
	"kusionstack.io/kusion-module-framework/pkg/server"
)

var FileExtensions = []string{".yaml", ".yml", ".json"}

func main() {
	server.Start(&K8sManifest{
		MergedPaths: make(map[string]bool),
	})
}

// K8sManifest implements the Kusion Module generator interface.
type K8sManifest struct {
	// Paths is a list of the paths of the YAML files, or the directories of the
	// raw Kubernetes manifests.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	// MergedPaths is a map of K8s manifest paths.
	MergedPaths map[string]bool `yaml:"mergedPaths,omitempty" json:"mergedPaths,omitempty"`
}

// Generate implements the generation logic of k8s_manifest module, which
// changes the raw K8s manifests into the Kusion Resources.
func (k *K8sManifest) Generate(ctx context.Context, request *module.GeneratorRequest) (response *module.GeneratorResponse, err error) {
	// Get the module logger with the generator context.
	logger := log.GetModuleLogger(ctx)
	logger.Info("Generating resources...")

	defer func() {
		if r := recover(); r != nil {
			logger.Debug("failed to generate k8s_manifest module: %v", r)
			response = nil
			rawRequest, _ := json.Marshal(request)
			err = fmt.Errorf("panic in k8s_manifest module generator but recovered with error: [%v] and stack %v and request %v",
				r, string(debug.Stack()), string(rawRequest))
		}
	}()

	if err := k.CompleteConfig(request.DevConfig, request.PlatformConfig); err != nil {
		logger.Debug("failed to get complete k8s_manifest module configs: %v", err)
		return nil, err
	}

	// 1. Get all of the YAML files (.yaml and .yml) in paths.
	// 2. Get all of the Kubernetes objects and append them into the Kusion Spec Resources.
	manifestYAMLFiles := make(map[string][]interface{})
	for path := range k.MergedPaths {
		pathInfo, err := os.Stat(path)
		if err != nil {
			return nil, err
		}

		if pathInfo.IsDir() {
			if err = filepath.WalkDir(path, func(filePath string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if ignoreFile(filePath, FileExtensions) {
					return nil
				}

				if err = appendManifest(filePath, manifestYAMLFiles); err != nil {
					return err
				}
				return nil
			}); err != nil {
				return nil, err
			}
		} else {
			if err = appendManifest(path, manifestYAMLFiles); err != nil {
				return nil, err
			}
		}
	}

	resources := []kusionapiv1.Resource{}
	for _, objList := range manifestYAMLFiles {
		for _, obj := range objList {
			if obj == nil {
				continue
			}

			apiVersion := obj.(map[string]interface{})["apiVersion"].(string)
			kind := obj.(map[string]interface{})["kind"].(string)
			metadata := obj.(map[string]interface{})["metadata"].(map[string]interface{})
			name := metadata["name"].(string)

			var namespace string
			if ns, ok := metadata["namespace"]; ok {
				namespace = ns.(string)
			}

			var kusionID string
			if namespace == "" {
				kusionID = apiVersion + ":" + kind + ":" + name
			} else {
				kusionID = apiVersion + ":" + kind + ":" + namespace + ":" + name
			}

			resources = append(resources, kusionapiv1.Resource{
				ID:         kusionID,
				Type:       kusionapiv1.Kubernetes,
				Attributes: obj.(map[string]interface{}),
			})
		}
	}

	return &module.GeneratorResponse{
		Resources: resources,
	}, nil
}

// appendManifest appends manifest objects in K8s YAML file to a map.
func appendManifest(filePath string, manifestYAMLFiles map[string][]interface{}) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}

	decoder := k8sYAML.NewYAMLOrJSONDecoder(f, 4096)
	for {
		data := make(map[string]interface{})
		if err := decoder.Decode(&data); err != nil {
			if err == io.EOF {
				return nil
			}

			return fmt.Errorf("error parsing %s: %v", filePath, err)
		}

		if len(data) == 0 {
			continue
		}

		manifestYAMLFiles[filePath] = append(manifestYAMLFiles[filePath], data)
	}
}

// ignoreFile indicates a filename is ended with specified extension or not
func ignoreFile(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return false
	}
	ext := filepath.Ext(path)
	for _, s := range extensions {
		if strings.EqualFold(s, ext) {
			return false
		}
	}
	return true
}

// CompleteConfig completes the k8s_manifest module configs with both devModuleConfig and platformModuleConfig.
func (k *K8sManifest) CompleteConfig(devConfig kusionapiv1.Accessory, platformConfig kusionapiv1.GenericConfig) error {
	// Retrieve the config items the developers are concerned about.
	if devConfig != nil {
		devCfgYAMLStr, err := yaml.Marshal(devConfig)
		if err != nil {
			return err
		}

		if err = yaml.Unmarshal(devCfgYAMLStr, k); err != nil {
			return err
		}

		for _, path := range k.Paths {
			k.MergedPaths[path] = true
		}
	}

	// Retrieve the config items the platform engineers care about.
	if platformConfig != nil {
		platformCfgYAMLStr, err := yaml.Marshal(platformConfig)
		if err != nil {
			return err
		}

		tmpK := &K8sManifest{}
		if err = yaml.Unmarshal(platformCfgYAMLStr, tmpK); err != nil {
			return err
		}

		for _, path := range tmpK.Paths {
			if k.MergedPaths[path] {
				continue
			}

			k.MergedPaths[path] = true
		}
	}

	return nil
}

// ValidateConfig validates the completed k8s_manifest module configs are valid or not.
func (k *K8sManifest) ValidateConfig() error {
	if len(k.MergedPaths) == 0 {
		return errors.New("k8s manifest paths should not be empty")
	}

	return nil
}
