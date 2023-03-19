package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v38/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
)

func getLatestReleaseTag(owner, repo, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	fmt.Println(url)
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

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get latest release tag: %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	err = json.NewDecoder(resp.Body).Decode(&release)
	if err != nil {
		return "", err
	}

	// Strip the "v" prefix from the tag name if it exists
	return release.TagName, nil
}
func UpdateChartVersion(chartName, owner, repo, filename, newVersion, branch, token string) error {

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
		os.Exit(3)
	}

	// Decode the file content from base64
	contentBytes, err := fileContent.GetContent()
	if err != nil {
		return fmt.Errorf("error decoding file content: %v", err)
	}

	// Update the YAML value

	// Convert contentBytes to a byte slice
	content := []byte(contentBytes)

	// Unmarshal the YAML content into a map
	values := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(content, &values); err != nil {
		return fmt.Errorf("error unmarshalling YAML: %v", err)
	}

	// Update the chart version
	values[chartName].(map[interface{}]interface{})["chartVersion"] = newVersion

	// Marshal the updated values back to YAML
	updatedContent, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("error marshalling YAML: %v", err)
	}
	// Create a new blob object for the updated content
	newBlob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
		Content:  github.String(string(updatedContent)),
		Encoding: github.String("utf-8"),
	})
	if err != nil {
		return fmt.Errorf("error creating blob: %v", err)
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
func main() {
	owner := os.Getenv("INPUT_GITHUB_USER")
	repo := os.Getenv("INPUT_GITHUB_REPO")
	token := os.Getenv("INPUT_GITHUB_TOKEN")
	release := os.Getenv("INPUT_RELEASE")
	chartName := os.Getenv("INPUT_CHART_NAME")
	chartType := os.Getenv("INPUT_CHART_TYPE")
	releaseRemoveString := os.Getenv("INPUT_RELEASE_REMOVE_STRING")

	tag, err := getLatestReleaseTag(owner, repo, token)
	if err != nil {
		fmt.Println("error: ", err)
	}
	if release < tag {
		if releaseRemoveString != "" {
			tag = strings.ReplaceAll(tag, releaseRemoveString, "")
		}
		fmt.Println("token: ", token)
		fmt.Println(release + "<" + tag)
		fmt.Println("update required newer release found")

		UpdateChartVersion(chartName, "loeken", "homelab", "deploy/argocd/bootstrap-"+chartType+"-apps/values.yaml.example", tag, "main", token)
		UpdateChartVersion(chartName, "loeken", "homelab-updater", "values-"+chartType+".yaml", tag, "main", token)

		fmt.Println(chartName, " chart version updated")
	}

	output := fmt.Sprintf("Hello %s", tag)

	fmt.Printf(`::set-output name=myOutput::%s`, output)
}
