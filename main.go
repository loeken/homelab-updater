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
func UpdateChartVersionWithPR(chartName, owner, repo, filename, parentBlock, subBlock, newVersion, branch, token string) error {

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
	fmt.Println(newBlob.SHA)
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

	if subBlock != "" {
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
	owner := os.Getenv("INPUT_GITHUB_USER")
	repo := os.Getenv("INPUT_GITHUB_REPO")
	token := os.Getenv("INPUT_GITHUB_TOKEN")
	// chart_version := os.Getenv("INPUT_CHART_VERSION")
	// chartName := os.Getenv("INPUT_CHART_NAME")
	// remoteChartName := os.Getenv("INPUT_REMOTE_CHART_NAME")
	// chartType := os.Getenv("INPUT_CHART_TYPE")
	// releaseRemoveString := os.Getenv("INPUT_RELEASE_REMOVE_STRING")

	// selfManagedImage := os.Getenv("INPUT_SELF_MANAGED_IMAGE")
	// selfManagedChart := os.Getenv("INPUT_SELF_MANAGED_CHART")

	tag, err := getLatestReleaseTag(owner, repo, token)
	if err != nil {
		fmt.Println("error: ", err)
	}

	/*
		if release < tag {
			if releaseRemoveString != "" {
				tag = strings.ReplaceAll(tag, releaseRemoveString, "")
			}
			fmt.Println("token: ", token)
			fmt.Println(release + "<" + tag)
			fmt.Println("update required newer release found")

			err := UpdateChartVersionWithPR(chartName, "loeken", "homelab", "deploy/argocd/bootstrap-"+chartType+"-apps/values.yaml.example", chartName, "chartVersion", tag, "main", token)
			if err != nil {
				fmt.Println("error encountered: ", err)
			}
			UpdateChartVersionWithPR(chartName, "loeken", "homelab-updater", "values-"+chartType+".yaml", chartName, "chartVersion", tag, "main", token)
			if err != nil {
				fmt.Println("error encountered: ", err)
			}
			if selfManagedImage == "true" {
				fmt.Println("self managed  Image: ", chartName, "loeken", "docker-"+chartName, ".github/workflows/release.yml", "env", "version", tag, "main", token)

				UpdateChartVersion(chartName, "loeken", "docker-"+chartName, "version.yaml", chartName, "env", "version", tag, "main", token)
				if err != nil {
					fmt.Println("error encountered: ", err)
				}
				fmt.Println("finishied")
			}
			if selfManagedChart == "true" {
				fmt.Println("self managed  chart: ", chartName, "loeken", "helm-charts", "charts/"+remoteChartName+"/Chart.yaml", "version", "", tag, "main", token)

				UpdateChartVersion(chartName, "loeken", "helm-charts", "charts/"+remoteChartName+"/Chart.yaml", "version", "", release, tag, "main", token)
				if err != nil {
					fmt.Println("error encountered: ", err)
				}
				fmt.Println("finishied")
			}

			fmt.Println(chartName, " chart version updated")
		}
	*/
	fmt.Printf("RELEASE=%s\n", tag)
}
