package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v53/github"
	"golang.org/x/oauth2"
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
	valuesChartName := os.Getenv("INPUT_VALUES_CHART_NAME")
	oldChartVersion := os.Getenv("INPUT_CHART_VERSION")
	// remoteChartName := os.Getenv("INPUT_REMOTE_CHART_NAME")
	// chartType := os.Getenv("INPUT_CHART_TYPE")
	releaseRemoveString := os.Getenv("INPUT_RELEASE_REMOVE_STRING")

	selfManagedImage := os.Getenv("INPUT_SELF_MANAGED_IMAGE")
	selfManagedChart := os.Getenv("INPUT_SELF_MANAGED_CHART")
	dockerTagPrefix := os.Getenv("INPUT_DOCKERTAGPREFIX")
	dockerTagSuffix := os.Getenv("INPUT_DOCKERTAGSUFFIX")

	var err error

	app_version, err := getLatestReleaseTag(owner, repo, token)
	chartInfo, err1 := getLatestChartVersion(chart_index_url, chartName)
	if err1 != nil {
		fmt.Println("error: ", err)
	}

	if err != nil {
		fmt.Println("error: ", err)
		if err.Error() == "failed to get latest tag: 404 Not Found" {
			app_version = chartInfo.Version
		}
	}
	app_version = strings.Replace(app_version, releaseRemoveString, "", -1)
	app_version = dockerTagPrefix + app_version + dockerTagSuffix
	fmt.Println("app version new src repo: " + app_version)

	chart_app_version := strings.Replace(chartInfo.AppVersion, releaseRemoveString, "", -1)
	chart_app_version = dockerTagPrefix + chart_app_version + dockerTagSuffix
	fmt.Println("app version in chart: " + chart_app_version)
	fmt.Println("chart version in src repo: " + chartInfo.Version)
	fmt.Println("current chart version my repo: " + oldChartVersion)

	fmt.Println(app_version)
	fmt.Println(oldChartVersion + "compare" + chartInfo.Version)

	if compareVersions(oldChartVersion, chartInfo.Version) < 0 {
		fmt.Println("new version found of chart")
		if compareVersions(chart_app_version, app_version) < 0 {
			if selfManagedImage == "true" {
				fmt.Println("new version found of self managed app found")

				err := UpdateChartVersion(
					chartName,
					"loeken",
					"docker-"+valuesChartName,
					"version.yaml",
					"env",
					"version",
					app_version,
					"main",
					token,
				)
				if err != nil {
					fmt.Println("error encountered: ", err)
				}
			}
			if selfManagedChart == "true" {
				fmt.Println("new version found of self managed chart found")

				err4 := UpdateHelmChartVersionsWithPR(
					chartName,
					"loeken",
					"helm-charts",
					"charts/"+chartName+"/Chart.yaml",
					extractVersion(app_version),
					app_version,
					"main",
					token,
				)
				if err4 != nil {
					fmt.Println("error encountered: ", err4)
				}
			}
		}
		// update homelab
		err1 := UpdateTargetRevision(valuesChartName, "loeken", "homelab", "deploy/argocd/bootstrap-optional-apps/templates/"+valuesChartName+".yaml", extractVersion(chartInfo.Version), "main", token)
		if err1 != nil {
			fmt.Println("error encountered: ", err1)
		}

		// update values in this repo
		err2 := UpdateChartVersionWithPR(valuesChartName, "loeken", "homelab-updater", "values-optional.yaml", valuesChartName, "chartVersion", extractVersion(chartInfo.Version), "main", token)
		if err2 != nil {
			fmt.Println("error encountered: ", err2)
		}

		os.Exit(1)
	} else {
		fmt.Println("chart is up2date")

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
func UpdateChartVersion(chartName, owner, repo, filename, parentBlock, subBlock, newVersion, branch, token string) error {

	client := &http.Client{}

	// GET request to fetch file contents
	fmt.Printf(fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, filename))
	getReq, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, filename), nil)
	if err != nil {
		return err
	}

	getReq.Header.Set("Authorization", "token "+token)

	getResp, err := client.Do(getReq)
	if err != nil {
		return err
	}
	defer getResp.Body.Close()

	getBody, err := ioutil.ReadAll(getResp.Body)
	if err != nil {
		return err
	}

	fmt.Println("GET request status: ", getResp.Status)
	fmt.Println("GET request body: ", string(getBody))

	// Unmarshal the response
	getRespMap := make(map[string]interface{})
	err = json.Unmarshal(getBody, &getRespMap)
	if err != nil {
		return err
	}

	// Decode content
	decodedContent, err := base64.StdEncoding.DecodeString(getRespMap["content"].(string))
	if err != nil {
		return err
	}

	yamlMap := make(map[interface{}]interface{})
	err = yaml.Unmarshal(decodedContent, &yamlMap)
	if err != nil {
		return err
	}

	env, ok := yamlMap["env"].(map[interface{}]interface{})
	if !ok {
		return fmt.Errorf("no env section found in the file")
	}
	env["version"] = newVersion

	updatedContent, err := yaml.Marshal(yamlMap)
	if err != nil {
		return err
	}
	fmt.Println("sha of file:", getRespMap["sha"])
	// Prepare request body for the PUT request
	putReqBody := map[string]interface{}{
		"message": "Update version to " + newVersion,
		"content": base64.StdEncoding.EncodeToString(updatedContent),
		"branch":  branch,
		"sha":     getRespMap["sha"],
		"committer": map[string]string{
			"name":  "loeken",
			"email": "loeken@internetz.me",
		},
	}
	putReqBodyBytes, err := json.Marshal(putReqBody)
	if err != nil {
		return err
	}

	// PUT request to update file
	putReq, err := http.NewRequest("PUT", fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, filename), bytes.NewBuffer(putReqBodyBytes))
	if err != nil {
		return err
	}

	putReq.Header.Set("Accept", "application/vnd.github+json")
	putReq.Header.Set("Authorization", "Bearer "+token)
	putReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := client.Do(putReq)
	if err != nil {
		return err
	}
	defer putResp.Body.Close()

	putBody, err := ioutil.ReadAll(putResp.Body)
	if err != nil {
		return err
	}

	fmt.Println("PUT request status: ", putResp.Status)
	fmt.Println("PUT request body: ", string(putBody))

	return nil
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
	//values := make(map[string]interface{})
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(content, &values); err != nil {
		fmt.Printf("error unmarshalling YAML: %v", err)
		return err
	}

	// Update the chart version
	values[parentBlock].(map[interface{}]interface{})[subBlock] = newVersion
	//values[parentBlock].(map[string]interface{})[subBlock] = newVersion

	// Marshal the updated values back to YAML
	updatedContent, err := yaml.Marshal(values)
	if err != nil {
		fmt.Printf("error marshalling YAML: %v", err)
		return err
	}
	fmt.Println(updatedContent)
	// Create a new blob object for the updated content
	newBlob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
		Content:  github.String(string(updatedContent)),
		Encoding: github.String("utf-8"),
	})
	if err != nil {
		fmt.Println("Error creating blob: ", err)
		return err
	}
	fmt.Println("New blob SHA:", *newBlob.SHA)

	// Get the latest commit object for the branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		fmt.Printf("error getting ref: %v", err)
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
func updateYAMLContent(values map[interface{}]interface{}, newVersion string, appVersion string) {
	// Update appVersion
	values["appVersion"] = appVersion

	// Update version
	values["version"] = newVersion

	// Update annotations
	if annotations, ok := values["annotations"].(map[interface{}]interface{}); ok {
		newVersion = strings.Replace(newVersion, "-", " ", -1)
		updatedChanges := fmt.Sprintf("- kind: changed\n  description: updated to %s", newVersion)
		annotations["artifacthub.io/changes"] = updatedChanges
	}
}
func extractVersion(input string) string {
	// Use regular expression to extract version patterns
	re := regexp.MustCompile(`(\d+\.\d+(\.\d+)?)`)
	matches := re.FindStringSubmatch(input)

	if len(matches) == 0 {
		return "" // Return an empty string if no matches found
	}

	versionParts := strings.Split(matches[1], ".")

	// Append ".0" for the missing parts to make it x.x.x format
	for len(versionParts) < 3 {
		versionParts = append(versionParts, "0")
	}

	// Return only the first 3 segments
	return strings.Join(versionParts[:3], ".")
}

func UpdateHelmChartVersionsWithPR(chartName, owner, repo, filename, newVersion, appVersion, branch, token string) error {
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

	// Convert contentBytes to a byte slice
	content := []byte(contentBytes)

	// Unmarshal the YAML content into a map
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(content, &values); err != nil {
		fmt.Printf("error unmarshalling YAML: %v", err)
		return err
	}

	// Update the specific blocks in the YAML
	updateYAMLContent(values, newVersion, appVersion)

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
		fmt.Println("Error creating blob: ", err)
		return err
	}

	// Get the latest commit object for the branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		fmt.Printf("error getting ref: %v", err)
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

func UpdateTargetRevision(chartName, owner, repo, filename, newVersion, branch, token string) error {
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
		return fmt.Errorf("error getting file content: %v", err)
	}

	// Decode the file content from base64
	contentBytes, err := fileContent.GetContent()
	if err != nil {
		return fmt.Errorf("error decoding file content: %v", err)
	}
	content := []byte(contentBytes)

	// Strip helm template wrappers and capture them
	re := regexp.MustCompile(`(?s)({{.*?}})\n(.+?)\n({{.*?}})`)
	matches := re.FindSubmatch(content)
	if matches == nil || len(matches) < 4 {
		return errors.New("couldn't find the expected YAML section")
	}
	beginWrapper := matches[1] // {{ if .Values.certmanager.enabled }}
	strippedContent := matches[2]
	endWrapper := matches[3] // {{ end }}

	// Unmarshal the stripped YAML content into a map
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(strippedContent, &values); err != nil {
		return fmt.Errorf("error unmarshalling YAML: %v", err)
	}

	// Update the targetRevision
	sourceBlock := values["spec"].(map[interface{}]interface{})["source"].(map[interface{}]interface{})
	sourceBlock["targetRevision"] = newVersion

	// Marshal the updated values back to YAML
	updatedContent, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("error marshalling YAML: %v", err)
	}

	// Re-add the wrappers to the updated content
	finalContent := append(beginWrapper, '\n')
	finalContent = append(finalContent, updatedContent...)
	finalContent = append(finalContent, '\n')
	finalContent = append(finalContent, endWrapper...)

	// Create a new blob object for the updated content using the finalContent
	newBlob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
		Content:  github.String(string(finalContent)),
		Encoding: github.String("utf-8"),
	})
	if err != nil {
		return fmt.Errorf("Error creating blob: %v", err)
	}

	// Get the latest commit object for the branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return fmt.Errorf("error getting ref: %v", err)
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
		return fmt.Errorf("error creating tree: %v", err)
	}

	// Create a new commit object with the updated tree object
	newCommit, _, err := client.Git.CreateCommit(ctx, owner, repo, &github.Commit{
		Message: github.String(fmt.Sprintf("Update %s to version %s", chartName, newVersion)),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: &parentSHA}},
	})
	if err != nil {
		return fmt.Errorf("error creating commit: %v", err)
	}

	// Create a new reference for the updated commit
	newBranch := fmt.Sprintf("refs/heads/update-%s-to-%s", chartName, newVersion)
	_, _, err = client.Git.CreateRef(ctx, owner, repo, &github.Reference{
		Ref:    github.String(newBranch),
		Object: &github.GitObject{SHA: newCommit.SHA},
	})
	if err != nil {
		return fmt.Errorf("error creating reference: %v", err)
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
		return fmt.Errorf("failed to create pull request: %v", err)
	}

	// Print the URL of the new pull request
	fmt.Printf("Created pull request %s\n", newPR.GetHTMLURL())

	return nil
}
