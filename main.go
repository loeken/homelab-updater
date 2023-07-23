package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-github/v38/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
)

type ChartVersion struct {
	Version string `yaml:"version"`
}

type Chart struct {
	Name     string         `yaml:"name"`
	Versions []ChartVersion `yaml:"versions"`
}

type ChartIndex struct {
	Entries map[string][]ChartVersion `yaml:"entries"`
}

func getLatestChartVersion(chartIndexURL, chartName string) (string, error) {
	resp, err := http.Get(chartIndexURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var index ChartIndex
	err = yaml.Unmarshal(body, &index)
	if err != nil {
		return "", err
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
				version := strings.Join(parts, ".")
				return version, nil
			}
		}
		return "", fmt.Errorf("no stable version found for chart %s", chartName)
	}

	return "", fmt.Errorf("chart %s not found", chartName)
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
func UpdateChartVersionWithPR(chartName, owner, repo, filename, parentBlock, subBlock, newVersion, branch, token string) error {

	fmt.Println(repo, chartName, filename, owner, branch)
	ctx := context.Background()

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	// Get the current contents of the file
	fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, repo, filename, &github.RepositoryContentGetOptions{
		Ref: branch,
	})
	if err != nil {

		fmt.Println("error getting file content:", err)
		return err
	}

	// Decode the file content from base64
	contentBytes, err := fileContent.GetContent()
	if err != nil {
		fmt.Printf("error decoding file content: %v", err)
		return err
	}

	// Update the YAML value

	// Convert contentBytes to a byte slice
	content := []byte(contentBytes)

	// Unmarshal the YAML content into a map
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(content, &values); err != nil {
		fmt.Printf("error unmarshalling YAML: %v", err)
		return err
	}

	// Update the chart version
	values[parentBlock].(map[interface{}]interface{})[subBlock] = newVersion

	// Marshal the updated values back to YAML
	updatedContent, err := yaml.Marshal(values)
	if err != nil {
		fmt.Printf("error marshalling YAML: %v", err)
		return err
	}
	// Create a new blob object for the updated content
	newBlob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
		Content:  github.String(string(updatedContent)),
		Encoding: github.String("utf-8"),
	})
	if err != nil {
		fmt.Printf("error creating blob: %v", err)
		return err
	}

	// Get the latest commit object for the branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		fmt.Printf("error getting ref: %v", err)
		return err
	}
	parentSHA := ref.Object.GetSHA()
	fmt.Println("filenmae path:")
	fmt.Println(fileContent.GetPath())
	fmt.Println(*newBlob.SHA)
	// Create a new tree object with the updated file
	newTree, _, err := client.Git.CreateTree(ctx, owner, repo, parentSHA, []*github.TreeEntry{
		{
			Path: github.String(fileContent.GetPath()),
			Mode: github.String("100644"),
			Type: github.String("blob"),
			SHA:  newBlob.SHA,
		},
	})
	if err != nil {
		fmt.Printf("error creating tree: %v", err)
		return err
	}

	// Create a new commit object with the updated tree object
	newCommit, _, err := client.Git.CreateCommit(ctx, owner, repo, &github.Commit{
		Message: github.String(fmt.Sprintf("Update %s to version %s", chartName, newVersion)),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: &parentSHA}},
	})
	if err != nil {
		fmt.Printf("error creating commit: %v", err)
		return err
	}

	// Create a new reference for the updated commit
	newBranch := fmt.Sprintf("refs/heads/update-%s-to-%s", chartName, newVersion)
	_, _, err = client.Git.CreateRef(ctx, owner, repo, &github.Reference{
		Ref:    github.String(newBranch),
		Object: &github.GitObject{SHA: newCommit.SHA},
	})
	if err != nil {
		fmt.Printf("error creating reference: %v", err)
		return err
	}

	// Create a pull request with the changes
	title := fmt.Sprintf("Update %s to version %s", chartName, newVersion)
	body := fmt.Sprintf("Update %s to version %s", chartName, newVersion)
	newPR, _, err := client.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: github.String(title),
		Body:  github.String(body),
		Head:  github.String(newBranch),
		Base:  github.String(branch),
	})
	if err != nil {
		fmt.Printf("failed to create pull request: %v", err)
		return err
	}

	// Print the URL of the new pull request
	fmt.Printf("Created pull request %s\n", newPR.GetHTMLURL())

	return nil
}
func UpdateChartVersion(chartName, owner, repo, filename, parentBlock, subBlock, oldVersion, newVersion, branch, token string) error {

	fmt.Println("we here")
	// create an authenticated github client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get the current contents of the file
	fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, repo, filename, &github.RepositoryContentGetOptions{
		Ref: branch,
	})
	if err != nil {
		fmt.Println(err)
		return err
	}

	// Decode the file content from base64
	contentBytes, err := fileContent.GetContent()
	if err != nil {
		return err
	}

	// Update the YAML value
	// Convert contentBytes to a byte slice
	content := []byte(contentBytes)

	// Unmarshal the YAML content into a map
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(content, &values); err != nil {
		return err
	}

	// Update the chart version
	fmt.Println(subBlock)
	if subBlock != "" {
		fmt.Println("newVersion:" + newVersion)
		values[parentBlock].(map[interface{}]interface{})[subBlock] = newVersion
	}
	// Marshal the updated values back to YAML
	updatedContent, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	if subBlock == "" {
		strings.ReplaceAll(string(updatedContent), oldVersion, newVersion)
	}
	// Create a new blob object for the updated content
	newBlob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
		Content:  github.String(string(updatedContent)),
		Encoding: github.String("utf-8"),
	})
	if err != nil {
		return err
	}

	// Get the latest commit object for the branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return err
	}
	parentSHA := ref.Object.GetSHA()

	// Create a new tree object with the updated file
	newTree, _, err := client.Git.CreateTree(ctx, owner, repo, parentSHA, []*github.TreeEntry{
		{
			Path: github.String(fileContent.GetPath()),
			Mode: github.String("100644"),
			Type: github.String("blob"),
			SHA:  newBlob.SHA,
		},
	})
	if err != nil {
		return err
	}

	// Create a new commit object with the updated tree object
	newCommit, _, err := client.Git.CreateCommit(ctx, owner, repo, &github.Commit{
		Message: github.String(fmt.Sprintf("Update %s to version %s", chartName, newVersion)),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: &parentSHA}},
	})
	if err != nil {
		return err
	}

	// Update the branch reference to the new commit
	_, _, err = client.Git.UpdateRef(ctx, owner, repo, &github.Reference{
		Ref: github.String(fmt.Sprintf("refs/heads/%s", branch)),
		Object: &github.GitObject{
			SHA: newCommit.SHA,
		},
	}, false)
	if err != nil {
		fmt.Printf("error updating reference: %v", err)
		return err
	}

	return nil
}
func main() {
	outputFile := "output.txt"

	owner := os.Getenv("INPUT_GITHUB_USER")
	repo := os.Getenv("INPUT_GITHUB_REPO")
	token := os.Getenv("INPUT_GITHUB_TOKEN")
	chart_index_url := os.Getenv("INPUT_CHART_INDEX_URL")

	chartName := os.Getenv("INPUT_CHART_NAME")
	valuesChartName := os.Getenv("INPUT_VALUES_CHART_NAME")
	oldChartVersion := os.Getenv("INPUT_CHART_VERSION")
	remoteChartName := os.Getenv("INPUT_REMOTE_CHART_NAME")
	chartType := os.Getenv("INPUT_CHART_TYPE")
	releaseRemoveString := os.Getenv("INPUT_RELEASE_REMOVE_STRING")

	selfManagedImage := os.Getenv("INPUT_SELF_MANAGED_IMAGE")
	selfManagedChart := os.Getenv("INPUT_SELF_MANAGED_CHART")
	dockerTagPrefix := os.Getenv("INPUT_DOCKERTAGPREFIX")
	dockerTagSuffix := os.Getenv("INPUT_DOCKERTAGSUFFIX")

	var chart_version string
	var err error

	app_version, err := getLatestReleaseTag(owner, repo, token)
	if err != nil {
		fmt.Println("error: ", err)
		if err.Error() == "failed to get latest tag: 404 Not Found" {
			app_version = chart_version
		}
	}
	app_version = strings.Replace(app_version, releaseRemoveString, "", -1)
	app_version = dockerTagPrefix + app_version + dockerTagSuffix
	if chart_index_url == "" {
		// try and get app Version:
		oldAppVersion, _ := GetAppVersionFromGitHubChart("loeken", "helm-charts", chartName, token)
		oldAppVersion = strings.ReplaceAll(oldAppVersion, releaseRemoveString, "")

		fmt.Println("oldAppVersioN" + oldAppVersion)

		if selfManagedImage == "true" {
			result := compareVersions(oldAppVersion, app_version)
			if result < 0 {

				fmt.Println("detected new version: " + app_version + " old version:" + oldAppVersion)
				fmt.Println(
					chartName,
					"loeken",
					"docker-"+chartName,
					".github/workflows/release.yml",
					"env",
					"version",
					oldAppVersion,
					app_version,
					"main",
					token,
				)
				err := UpdateChartVersion(
					chartName,
					"loeken",
					"docker-"+chartName,
					".github/workflows/release.yml",
					"env",
					"version",
					oldAppVersion,
					app_version,
					"main",
					token,
				)
				if err != nil {
					fmt.Println("error encountered: ", err)
				}

				fmt.Println("finishied")
				os.Exit(1)
			}
			chart_version = oldChartVersion
		}

	} else {
		chart_version, err = getLatestChartVersion(chart_index_url, chartName)
		if err != nil {
			fmt.Println("error: ", err)

		}
	}
	fmt.Println("chart:", chart_version)
	fmt.Println("app:", app_version)
	fmt.Println("old chart version:", oldChartVersion)

	fmt.Println(oldChartVersion + "<" + chart_version)

	result := compareVersions(oldChartVersion, chart_version)
	if result < 0 {

		fmt.Println("update required newer release found")

		if selfManagedImage == "true" {

			UpdateChartVersion(chartName, "loeken", "docker-"+chartName, "version.yaml", valuesChartName, "env", "version", chart_version, "main", token)
			if err != nil {
				fmt.Println("error encountered: ", err)
			}
			fmt.Println("finishied")
		}

		if selfManagedChart == "true" {
			fmt.Println("self managed  chart: ", chartName, "loeken", "helm-charts", "charts/"+remoteChartName+"/Chart.yaml", "version", "", oldChartVersion, chart_version, "main", token)
			UpdateChartVersion(chartName, "loeken", "helm-charts", "charts/"+remoteChartName+"/Chart.yaml", "version", "", oldChartVersion, chart_version, "main", token)
			if err != nil {
				fmt.Println("error encountered: ", err)
			}
			UpdateChartVersion(chartName, "loeken", "helm-charts", "charts/"+remoteChartName+"/Chart.yaml", "appVersion", "", oldChartVersion, chart_version, "main", token)
			if err != nil {
				fmt.Println("error encountered: ", err)
			}
			fmt.Println("finishied")
		}

		// update homelab
		err := UpdateChartVersionWithPR(valuesChartName, "loeken", "homelab", "deploy/argocd/bootstrap-"+chartType+"-apps/values.yaml.example", valuesChartName, "chartVersion", chart_version, "main", token)
		if err != nil {
			fmt.Println("error encountered: ", err)
		}

		// update values in this repo
		UpdateChartVersionWithPR(valuesChartName, "loeken", "homelab-updater", "values-"+chartType+".yaml", valuesChartName, "chartVersion", chart_version, "main", token)
		if err != nil {
			fmt.Println("error encountered: ", err)
		}

		fmt.Println(chartName, " chart version updated")
	} else {
		fmt.Println("else")
	}

	f, err := os.Create(outputFile)
	if err != nil {
		fmt.Println("error: ", err)
	}
	defer f.Close()

	_, err = f.WriteString(fmt.Sprintf("LATEST_APP_RELEASE=%s\n", app_version))
	if err != nil {
		fmt.Println("error: ", err)
	}
	_, err = f.WriteString(fmt.Sprintf("LATEST_CHART_RELEASE=%s\n", chart_version))
	if err != nil {
		fmt.Println("error: ", err)
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

type RemoteChart struct {
	AppVersion string `yaml:"appVersion"`
}

type GitHubContent struct {
	Content string `json:"content"`
}

func GetFileContentFromGitHub(owner, repo, path, token string) (string, error) {
	// Build the request URL
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	// Create a new request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	// Set the Authorization header
	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Parse the response
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get file content: %s", resp.Status)
	}

	var content GitHubContent
	err = json.NewDecoder(resp.Body).Decode(&content)
	if err != nil {
		return "", err
	}

	// The content is base64 encoded, so it needs to be decoded
	decoded, err := base64.StdEncoding.DecodeString(content.Content)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}
func GetAppVersionFromGitHubChart(owner, repo, chartName, token string) (string, error) {
	// Build the path to the Chart.yaml file
	path := fmt.Sprintf("charts/%s/Chart.yaml", chartName)

	// Get the file content from GitHub
	content, err := GetFileContentFromGitHub(owner, repo, path, token)
	if err != nil {
		return "", err
	}
	// Parse the YAML content
	var chart RemoteChart
	err = yaml.Unmarshal([]byte(content), &chart)
	if err != nil {
		return "", err
	}

	return chart.AppVersion, nil
}
