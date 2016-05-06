// "gitbackup" holds the functions that do the actual backing up of git
// repositories.
package gitbackup

import(
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type repository struct {
	name string
	cloneURL string
}

// BackupTarget backs up an entity that holds one or more git repositories and
// has an interface to retrieve that list of repositories.
// Examples of entities include:
//   - A GitHub user.
//   - A BitBucket user.
//   - A GitHub organization.
func BackupTarget(target map[string]string, backupDirectory string) error {
	log.Printf(`Backing up target "%s"`, target["name"])

	// Retrieve a list of all the git repositories available from the target.
	var repoList []repository
	var err error
	switch target["source"] {
	case "github":
		repoList, err = getGitHubRepoList(target, backupDirectory)
	case "bitbucket":
		repoList, err = getBitBucketRepoList(target, backupDirectory)
	default:
		err = fmt.Errorf(`"%s" is not a recognized source type`, target["source"])
	}
	if (err != nil) {
		return err
	}

	// Back up each repository found.
	for _, repo := range repoList {
		backupRepository(
			target["name"],
			repo.name,
			repo.cloneURL,
			backupDirectory,
		)
	}

	return nil
}

// getGitHubRepoList finds all the repositories belonging to a given user or
// organization on GitHub.
func getGitHubRepoList(target map[string]string, backupDirectory string) ([]repository, error) {
	// Create URL to request list of repos.
	var requestURL string = fmt.Sprintf(
		"https://api.github.com/%s/%s/repos?access_token=%s&per_page=200",
		target["type"],
		target["entity"],
		target["token"],
	)

	// Retrieve list of repositories.
	response, err := http.Get(requestURL)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect with the source to retrieve the list of repositories: %s", err)
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve the list of repositories: %s", err)
	}

	// Parse JSON response.
	var dat []map[string]interface{}
	if err := json.Unmarshal(contents, &dat); err != nil {
		return nil, fmt.Errorf("Failed to parse JSON: %s", err)
	}

	// Make a list of repositories.
	repoList := make([]repository, len(dat))
	for i, repo := range dat {
		repoName, _ := repo["name"].(string)
		cloneURL, _ := repo["clone_url"].(string)
		cloneURL = strings.Replace(
			cloneURL,
			"https://",
			fmt.Sprintf("https://%s:%s@", target["entity"], target["token"]),
			1,
		)
		repoList[i] = repository{name: repoName, cloneURL: cloneURL}
	}

	// No errors.
	return repoList, nil
}

// getBitBucketRepoList finds all the repositories belonging to a given user on
// BitBucket.
func getBitBucketRepoList(target map[string]string, backupDirectory string) ([]repository, error) {
	// Create URL to request list of repos.
	// TODO: support pagination.
	var requestURL string = fmt.Sprintf(
		"https://%s:%s@bitbucket.org/api/2.0/repositories/%s?page=1&pagelen=100",
		target["entity"],
		target["password"],
		target["entity"],
	)

	// Retrieve list of repositories.
	response, err := http.Get(requestURL)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect with the source to retrieve the list of repositories: %s", err)
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve the list of repositories: %s", err)
	}

	// Parse JSON response.
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return nil, fmt.Errorf("Failed to parse JSON: %s", err)
	}
	var data []map[string]json.RawMessage
	if err := json.Unmarshal(metadata["values"], &data); err != nil {
		return nil, fmt.Errorf("Failed to parse JSON: %s", err)
	}

	// Make a list of repositories.
	repoList := make([]repository, len(data))
	for i, repo := range data {
		// Parse the remaining JSON message that pertains to this repository.
		var repoName string
		if err := json.Unmarshal(repo["name"], &repoName); err != nil {
			return nil, fmt.Errorf("Failed to parse JSON: %s", err)
		}
		var links map[string]json.RawMessage
		if err := json.Unmarshal(repo["links"], &links); err != nil {
			return nil, fmt.Errorf("Failed to parse JSON: %s", err)
		}
		var cloneLinks []map[string]string
		if err := json.Unmarshal(links["clone"], &cloneLinks); err != nil {
			return nil, fmt.Errorf("Failed to parse JSON: %s", err)
		}

		// Find the https URL to use for cloning.
		var cloneURL string
		for _, link := range cloneLinks {
			if link["name"] == "https" {
				cloneURL = link["href"]
			}
		}
		if cloneURL == "" {
			return nil, fmt.Errorf("Could not determine HTTPS cloning URL: %s", cloneLinks)
		}

		// Determine URL for cloning.
		cloneURL = strings.Replace(
			cloneURL,
			fmt.Sprintf("https://%s@", target["entity"]),
			fmt.Sprintf("https://%s:%s@", target["entity"], target["password"]),
			1,
		)

		repoList[i] = repository{name: repoName, cloneURL: cloneURL}
	}

	// No errors.
	return repoList, nil
}

// backupRepository takes a remote git repository and backs it up locally.
// Note that this makes a mirror repository - in other words, the backup only
// contains the content of a normal .git repository but no working directory,
// which saves space. You can always get a normal repository from the backup by
// doing a normal git clone of the backup itself.
func backupRepository(targetName string, repoName string, cloneURL string, backupDirectory string) {
	var cloneDirectory string = filepath.Join(backupDirectory, targetName, repoName)
	fmt.Println(fmt.Sprintf("#> %s", repoName))
	log.Printf(`Backing up repo "%s"`, repoName)

	if _, err := os.Stat(cloneDirectory); os.IsNotExist(err) {
		// The repo doesn't exist locally, clone it.
		log.Printf("Cloning %s to %s", cloneURL, cloneDirectory)

		cmd := exec.Command("git", "clone", "--mirror", cloneURL, cloneDirectory)
		cmdOut, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Error cloning the repository:", err)
		} else {
			fmt.Println("Cloned repository.")
			if len(cmdOut) > 0 {
				fmt.Printf(string(cmdOut))
			}
		}
	} else {
		// The repo already exists, pull updates.
		log.Printf("Pulling git repo in %s", cloneDirectory)

		cmd := exec.Command("git", "fetch", "-p", cloneURL)
		cmd.Dir = cloneDirectory
		cmdOut, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Error pulling in the repository:", err)
		} else {
			// Display pulled information.
			fmt.Println("Pulled latest updates in the repository.")
			if len(cmdOut) > 0 {
				fmt.Printf(string(cmdOut))
			}
		}
	}
}
