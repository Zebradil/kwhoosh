package myks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	yaml "gopkg.in/yaml.v3"
)

type HelmSyncer struct {
	ident string
}

func (hr *HelmSyncer) Ident() string {
	return hr.ident
}

func (hr *HelmSyncer) GenerateSecrets(_ *Globe) (string, error) {
	return "", nil
}

func (hr *HelmSyncer) Sync(a *Application, _ string) error {
	helmConfig, err := hr.getHelmConfig(a)
	if err != nil {
		log.Warn().Err(err).Msg(a.Msg(hr.getStepName(), "Unable to get helm config"))
		return err
	}

	if !helmConfig.BuildDependencies {
		log.Debug().Msg(a.Msg(hr.getStepName(), ".helm.buildDependencies is disabled, skipping"))
		return nil
	}

	chartsDir := a.expandVendorPath(a.e.g.HelmChartsDirName)
	files, err := os.ReadDir(chartsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		log.Debug().Msg(a.Msg(hr.getStepName(), "No Helm charts found"))
		return nil
	}
	for _, file := range files {
		chartDir := filepath.Join(chartsDir, file.Name())
		if err = ensureValidChartEntry(chartDir); err != nil {
			log.Warn().Err(err).Msg(a.Msg(hr.getStepName(), "Skipping invalid chart entry"))
			continue
		}

		if err = hr.helmBuild(a, chartDir); err != nil {
			return err
		}
	}
	log.Info().Msg(a.Msg(hr.getStepName(), "Synced"))
	return nil
}

func (hr *HelmSyncer) helmBuild(a *Application, chartDir string) error {
	chartPath := filepath.Join(chartDir, "Chart.yaml")
	if exists, _ := isExist(chartDir); !exists {
		return fmt.Errorf("can't locate Chart.yaml at: %s", chartPath)
	}

	chart, err := unmarshalYamlToMap(chartPath)
	if err != nil {
		return fmt.Errorf("failure to unmarshal Chart.yaml at: %s", chartPath)
	}

	dependencies, ok := chart["dependencies"]
	if !ok {
		return nil
	}

	helmCache := a.expandServicePath("helm-cache")
	cacheArgs := []string{
		"--repository-cache", filepath.Join(helmCache, "repository"),
		"--repository-config", filepath.Join(helmCache, "repositories.yaml"),
	}
	for _, dependency := range dependencies.([]interface{}) {
		depMap := dependency.(map[string]interface{})
		repo := depMap["repository"].(string)
		if strings.HasPrefix(repo, "http") {
			args := []string{"repo", "add", createURLSlug(repo), repo, "--force-update"}
			_, err = a.runCmd(hr.getStepName(), "helm repo add", "helm", nil, append(args, cacheArgs...))
			if err != nil {
				return fmt.Errorf("failed to add repository %s in %s ", repo, chartPath)
			}
		}
	}

	buildArgs := []string{"dependencies", "build", chartDir, "--skip-refresh"}
	_, err = a.runCmd(hr.getStepName(), "helm dependencies build", "helm", nil, append(buildArgs, cacheArgs...))
	if err != nil {
		return fmt.Errorf("failed to build dependencies for chart %s", chartDir)
	}
	return nil
}

func (hr *HelmSyncer) getHelmConfig(a *Application) (HelmConfig, error) {
	dataValuesYaml, err := a.ytt(hr.getStepName(), "get helm config", a.yttDataFiles, "--data-values-inspect")
	if err != nil {
		return HelmConfig{}, err
	}

	var helmConfig struct {
		Helm HelmConfig
	}
	err = yaml.Unmarshal([]byte(dataValuesYaml.Stdout), &helmConfig)
	if err != nil {
		log.Warn().Err(err).Msg(a.Msg(hr.getStepName(), "Unable to unmarshal data values"))
		return HelmConfig{}, err
	}

	return helmConfig.Helm, nil
}

func (hr *HelmSyncer) getStepName() string {
	return fmt.Sprintf("%s-%s", syncStepName, hr.Ident())
}

func ensureValidChartEntry(entryPath string) error {
	if entryPath == "" {
		return fmt.Errorf("empty entry path")
	}

	fileInfo, err := os.Stat(entryPath)
	if err != nil {
		return err
	}
	canonicName := entryPath
	if fileInfo.Mode()&os.ModeSymlink == 1 {
		if name, readErr := os.Readlink(entryPath); readErr != nil {
			return readErr
		} else {
			canonicName = name
		}
	}

	fileInfo, err = os.Stat(canonicName)
	if err != nil {
		return err
	}

	if !fileInfo.IsDir() {
		return fmt.Errorf("non-directory entry")
	}

	if exists, err := isExist(filepath.Join(canonicName, "Chart.yaml")); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("no Chart.yaml found")
	}

	return nil
}
