package local

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/drud/drud-go/utils"
	"github.com/drud/drud-go/utils/try"
	"github.com/fsouza/go-dockerclient"
	"github.com/gosuri/uitable"
)

// PrepLocalSiteDirs creates a site's directories for local dev in ~/.drud/client/site
func PrepLocalSiteDirs(base string) error {
	err := os.MkdirAll(base, os.FileMode(int(0774)))
	if err != nil {
		return err
	}

	dirs := []string{
		"src",
		"files",
		"data",
	}
	for _, d := range dirs {
		dirPath := path.Join(base, d)
		err := os.Mkdir(dirPath, os.FileMode(int(0774)))
		if err != nil {
			if !strings.Contains(err.Error(), "file exists") {
				return err
			}
		}
	}

	return nil
}

// WriteLocalAppYAML writes docker-compose.yaml to $HOME/.drud/app.Path()
func WriteLocalAppYAML(app App) error {
	homedir, err := utils.GetHomeDir()
	if err != nil {
		log.Fatalln(err)
	}

	basePath := path.Join(homedir, ".drud", app.RelPath())

	f, err := os.Create(path.Join(basePath, "docker-compose.yaml"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	rendered, err := app.RenderComposeYAML()
	if err != nil {
		return err
	}
	f.WriteString(rendered)
	return nil
}

// CloneSource clones or pulls a repo
func CloneSource(app App) error {
	homedir, err := utils.GetHomeDir()
	if err != nil {
		log.Fatalln(err)
	}

	details, err := app.GetRepoDetails()
	if err != nil {
		return err
	}

	coneURL, err := details.GetCloneURL()
	if err != nil {
		return err
	}

	basePath := path.Join(homedir, ".drud", app.RelPath(), "src")

	out, err := utils.RunCommand("git", []string{
		"clone", "-b", details.Branch, coneURL, basePath,
	})
	if err != nil {
		if !strings.Contains(string(out), "already exists") {
			return fmt.Errorf("%s - %s", err.Error(), string(out))
		}

		fmt.Print("Local copy of site exists, updating... ")

		out, err = utils.RunCommand("git", []string{
			"-C", basePath,
			"pull", "origin", details.Branch,
		})
		if err != nil {
			return fmt.Errorf("%s - %s", err.Error(), string(out))
		}

		fmt.Printf("Updated to latest in %s branch\n", details.Branch)
	}

	if len(out) > 0 {
		log.Info(string(out))
	}

	return nil
}

func GetPort(name string) (int64, error) {
	client, _ := GetDockerClient()
	var publicPort int64

	containers, err := client.ListContainers(docker.ListContainersOptions{All: false})
	if err != nil {
		return publicPort, err
	}

	for _, ctr := range containers {
		if strings.Contains(ctr.Names[0][1:], name) {
			for _, port := range ctr.Ports {
				if port.PublicPort != 0 {
					publicPort = port.PublicPort
					return publicPort, nil
				}
			}
		}
	}
	return publicPort, fmt.Errorf("%s container not ready", name)
}

// GetPodPort clones or pulls a repo
func GetPodPort(name string) (int64, error) {
	var publicPort int64

	err := try.Do(func(attempt int) (bool, error) {
		var err error
		publicPort, err = GetPort(name)
		if err != nil {
			time.Sleep(2 * time.Second) // wait a couple seconds
		}
		return attempt < 70, err
	})
	if err != nil {
		return publicPort, err
	}

	return publicPort, nil
}

// GetDockerClient returns a docker client for a docker-machine.
func GetDockerClient() (*docker.Client, error) {
	// Create a new docker client talking to the default docker-machine.
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		log.Fatal(err)
	}
	return client, err
}

func FilterNonDrud(vs []docker.APIContainers) []docker.APIContainers {
	homedir, err := utils.GetHomeDir()
	if err != nil {
		log.Fatalln(err)
	}

	var vsf []docker.APIContainers
	for _, v := range vs {
		clientName := strings.Split(v.Names[0][1:], "-")[0]
		if _, err = os.Stat(path.Join(homedir, ".drud", clientName)); os.IsNotExist(err) {
			continue
		}
		vsf = append(vsf, v)
	}
	return vsf
}

func FilterNonLegacy(vs []docker.APIContainers) []docker.APIContainers {

	var vsf []docker.APIContainers
	for _, v := range vs {
		container := v.Names[0][1:]

		if !strings.HasPrefix(container, "legacy-") {
			continue
		}

		vsf = append(vsf, v)
	}
	return vsf
}

func FilterNonLegacyFiles(files []os.FileInfo) []os.FileInfo {

	var filtered []os.FileInfo
	for _, v := range files {
		name := v.Name()
		parts := strings.SplitN(name, "-", 2)

		if len(parts) != 2 || !IsValidLegacyEnv(parts[1]) {
			continue
		}

		filtered = append(filtered, v)
	}
	return filtered
}

func IsValidLegacyEnv(s string) bool {
	envs := []string{"default", "staging", "production"}
	var valid bool

	for _, e := range envs {
		if s == e {
			valid = true
			break
		}
	}

	return valid
}

// FormatPlural is a simple wrapper which returns different strings based on the count value.
func FormatPlural(count int, single string, plural string) string {
	if count == 1 {
		return single
	}
	return plural
}

// SiteList will prepare and render a list of drud sites running locally.
func SiteList(containers []docker.APIContainers) error {
	legacy, local := map[string]LegacyApp{}, map[string]LegacyApp{}

	for _, container := range containers {
		for _, containerName := range container.Names {
			if strings.HasPrefix(containerName[1:], "legacy-") {
				ProcessContainer(legacy, containerName[1:], container)
				break
			}
			if strings.HasSuffix(containerName[1:], "-db") || strings.HasSuffix(containerName[1:], "-web") {
				ProcessContainer(local, containerName[1:], container)
				break
			}
		}
	}

	if len(legacy) > 0 {
		RenderAppTable(legacy, "legacy")
	}

	if len(local) > 0 {
		RenderAppTable(local, "local")
	}

	if len(local) == 0 && len(legacy) == 0 {
		fmt.Println("No applications found.")
	}

	return nil
}

// RenderAppTable will format a table for user display based on a list of apps.
func RenderAppTable(apps map[string]LegacyApp, name string) {
	if len(apps) > 0 {
		fmt.Printf("%v %s %v found.\n", len(apps), name, FormatPlural(len(apps), "site", "sites"))
		table := uitable.New()
		table.MaxColWidth = 200
		table.AddRow("NAME", "ENVIRONMENT", "TYPE", "URL", "DATABASE URL", "STATUS")

		for _, site := range apps {
			site.AddRow(table)
		}
		fmt.Println(table)
	}

}

// ProcessContainer will process a docker container for an app listing.
// Since apps contain multiple containers, ProcessContainer will be called once per container.
func ProcessContainer(l map[string]LegacyApp, containerName string, container docker.APIContainers) {
	parts := strings.Split(containerName, "-")

	if len(parts) == 4 {
		appid := parts[1] + "-" + parts[2]

		_, exists := l[appid]
		if exists == false {
			l[appid] = LegacyApp{
				Name:        parts[1],
				Environment: parts[2],
				Status:      container.State,
			}
		}
		app := l[appid]

		var publicPort int64
		for _, port := range container.Ports {
			if port.PublicPort != 0 {
				publicPort = port.PublicPort
			}
		}

		if parts[3] == "web" {
			app.WebPublicPort = publicPort
		}

		if parts[3] == "db" {
			app.DbPublicPort = publicPort
		}

		if container.State != "running" {
			app.Status = container.State
		}
		l[appid] = app
	}
}

// DetermineAppType uses some predetermined file checks to determine if a local app
// is of any of the known types
func DetermineAppType(basePath string) (string, error) {
	defaultLocations := map[string]string{
		"docroot/scripts/drupal.sh": "drupal",
		"docroot/wp":                "wp",
	}

	for k, v := range defaultLocations {
		if FileExists(path.Join(basePath, "src", k)) {
			return v, nil
		}
	}

	return "", fmt.Errorf("Couldn't determine app's type!")
}

// FileExists checks a file's existence
// @todo replace this with drud-go/utils version when merged
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// EnsureDockerRouter ensures the router is running.
func EnsureDockerRouter() {
	homeDir, err := utils.GetHomeDir()
	if err != nil {
		log.Fatal("could not find home directory")
	}
	dest := path.Join(homeDir, ".drud", "router-compose.yaml")
	f, ferr := os.Create(dest)
	if ferr != nil {
		log.Fatal(ferr)
	}
	defer f.Close()

	template := fmt.Sprintf(DrudRouterTemplate)
	f.WriteString(template)

	// run docker-compose up -d in the newly created directory
	out, err := utils.RunCommand("docker-compose", []string{"-f", dest, "up", "-d"})
	if err != nil {
		fmt.Println(fmt.Errorf("%s - %s", err.Error(), string(out)))
	}

}