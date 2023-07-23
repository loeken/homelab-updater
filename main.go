package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type ChartVersion struct {
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

type Chart struct {
	Name     string         `yaml:"name"`
	Versions []ChartVersion `yaml:"versions"`
}

type ChartIndex struct {
	Entries map[string][]ChartVersion `yaml:"entries"`
}

func main() {
	// outputFile := "output.txt"

	owner := os.Getenv("INPUT_GITHUB_USER")
	repo := os.Getenv("INPUT_GITHUB_REPO")
	token := os.Getenv("INPUT_GITHUB_TOKEN")
	chart_index_url := os.Getenv("INPUT_CHART_INDEX_URL")

	chartName := os.Getenv("INPUT_CHART_NAME")
	// valuesChartName := os.Getenv("INPUT_VALUES_CHART_NAME")
	oldChartVersion := os.Getenv("INPUT_CHART_VERSION")
	// remoteChartName := os.Getenv("INPUT_REMOTE_CHART_NAME")
	// chartType := os.Getenv("INPUT_CHART_TYPE")
	releaseRemoveString := os.Getenv("INPUT_RELEASE_REMOVE_STRING")

	// selfManagedImage := os.Getenv("INPUT_SELF_MANAGED_IMAGE")
	// selfManagedChart := os.Getenv("INPUT_SELF_MANAGED_CHART")
	dockerTagPrefix := os.Getenv("INPUT_DOCKERTAGPREFIX")
	dockerTagSuffix := os.Getenv("INPUT_DOCKERTAGSUFFIX")

	var chartVersion string
	var err error

	app_version, err := getLatestReleaseTag(owner, repo, token)
	if err != nil {
		fmt.Println("error: ", err)
		if err.Error() == "failed to get latest tag: 404 Not Found" {
			app_version = chartVersion
		}
	}
	app_version = strings.Replace(app_version, releaseRemoveString, "", -1)
	app_version = dockerTagPrefix + app_version + dockerTagSuffix
	fmt.Println("repo app version: " + app_version)
	chartInfo, err1 := getLatestChartVersion(chart_index_url, chartName)
	if err1 != nil {
		fmt.Println("error: ", err)
	}
	chart_app_version := strings.Replace(chartInfo.AppVersion, releaseRemoveString, "", -1)
	chart_app_version = dockerTagPrefix + chart_app_version + dockerTagSuffix
	fmt.Println("app version in chart: " + chart_app_version)
	fmt.Println("chart version: " + chartInfo.Version)
	fmt.Println("current chart version in repo: " + oldChartVersion)
	if compareVersions(oldChartVersion, chartVersion) < 0 {
		fmt.Println("new version found of chart")
		os.Exit(1)
	} else {
		if compareVersions(chart_app_version, app_version) < 0 {
			fmt.Println("new version found of app found")
			os.Exit(1)
		}
	}
}
func compareVersions(version1, version2 string) int {
	parts1 := strings.Split(version1, ".")
	parts2 := strings.Split(version2, ".")

	// Ensure both versions have the same number of components
	maxParts := len(parts1)
	if len(parts2) > maxParts {
		maxParts = len(parts2)
	}

	// Compare each component numerically
	for i := 0; i < maxParts; i++ {
		num1 := 0
		num2 := 0

		if i < len(parts1) {
			num1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			num2, _ = strconv.Atoi(parts2[i])
		}

		if num1 < num2 {
			return -1
		} else if num1 > num2 {
			return 1
		}
	}

	// All components are equal
	return 0
}

func getLatestChartVersion(chartIndexURL, chartName string) (*ChartVersion, error) {

	resp, err := http.Get(chartIndexURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var index ChartIndex
	err = yaml.Unmarshal(body, &index)
	if err != nil {
		return nil, err
	}

	// Find the latest stable version of the specified chart
	if versions, ok := index.Entries[chartName]; ok {
		for _, version := range versions {
			if !strings.Contains(version.Version, "alpha") && !strings.Contains(version.Version, "beta") {
				strippedTag := strings.TrimPrefix(version.Version, "v")

				parts := strings.Split(strippedTag, ".")
				if len(parts) > 3 {
					parts = parts[:3]
				}
				versionStr := strings.Join(parts, ".")
				return &ChartVersion{Version: versionStr, AppVersion: version.AppVersion}, nil
			}
		}
		return nil, fmt.Errorf("no stable version found for chart %s", chartName)
	}

	return nil, fmt.Errorf("chart %s not found", chartName)
}
func getLatestReleaseTag(owner, repo, token string) (string, error) {
	// Try to get the latest release first
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth("loeken", token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var release struct {
			TagName string `json:"tag_name"`
		}
		err = json.NewDecoder(resp.Body).Decode(&release)
		if err != nil {
			return "", err
		}

		// Strip the "v" prefix from the tag name if it exists
		strippedTag := strings.TrimPrefix(release.TagName, "v")
		return strippedTag, nil
	} else if resp.StatusCode == http.StatusNotFound {
		// If no releases found, get the latest tag
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/tags", owner, repo)
		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		req.SetBasicAuth("loeken", token)

		resp, err = client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to get latest tag: %s", resp.Status)
		}

		var tags []struct {
			Name string `json:"name"`
		}
		err = json.NewDecoder(resp.Body).Decode(&tags)
		if err != nil {
			return "", err
		}

		if len(tags) == 0 {
			return "", fmt.Errorf("no tags found")
		}

		// Strip the "v" prefix from the tag name if it exists
		strippedTag := strings.TrimPrefix(tags[0].Name, "v")
		return strippedTag, nil
	} else {
		return "", fmt.Errorf("failed to get latest release tag: %s", resp.Status)
	}
}
