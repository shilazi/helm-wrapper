package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/flock"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/cmd/helm/search"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/helmpath"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"
)

const searchMaxScore = 25

type repoChartElement struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"app_version"`
	Description string `json:"description"`
}

type repoChartList []repoChartElement

func applyConstraint(version string, versions bool, res []*search.Result) ([]*search.Result, error) {
	if len(version) == 0 {
		return res, nil
	}

	constraint, err := semver.NewConstraint(version)
	if err != nil {
		return res, errors.Wrap(err, "an invalid version/constraint format")
	}

	data := res[:0]
	foundNames := map[string]bool{}
	for _, r := range res {
		if _, found := foundNames[r.Name]; found {
			continue
		}
		v, err := semver.NewVersion(r.Chart.Version)
		if err != nil || constraint.Check(v) {
			data = append(data, r)
			if !versions {
				foundNames[r.Name] = true // If user hasn't requested all versions, only show the latest that matches
			}
		}
	}

	return data, nil
}

func buildSearchIndex(version string) (*search.Index, error) {
	i := search.NewIndex()
	for _, re := range helmConfig.HelmRepos {
		n := re.Name
		f := filepath.Join(settings.RepositoryCache, helmpath.CacheIndexFile(n))
		ind, err := repo.LoadIndexFile(f)
		if err != nil {
			glog.Warningf("WARNING: Repo %q is corrupt or missing. Try 'helm repo update'.", n)
			continue
		}

		i.AddRepo(n, ind, len(version) > 0)
	}
	return i, nil
}

func initRepos(c *repo.Entry) error {
	// Ensure the file directory exists as it is required for file locking
	err := os.MkdirAll(filepath.Dir(settings.RepositoryConfig), os.ModePerm)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Acquire a file lock for process synchronization
	fileLock := flock.New(strings.Replace(settings.RepositoryConfig, filepath.Ext(settings.RepositoryConfig), ".lock", 1))
	lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, time.Second)
	if err == nil && locked {
		SafeCloser(fileLock, &err)
	}
	if err != nil {
		return err
	}

	b, err := ioutil.ReadFile(settings.RepositoryConfig)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var f repo.File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return err
	}

	r, err := repo.NewChartRepository(c, getter.All(settings))
	if err != nil {
		return err
	}

	if _, err := r.DownloadIndexFile(); err != nil {
		return err
	}

	f.Update(c)

	if err := f.WriteFile(settings.RepositoryConfig, 0644); err != nil {
		return err
	}

	return nil
}

func updateRepos(c *gin.Context) {
	// reread config
	_, err := readRepoConfig()
	if err != nil {
		respErr(c, fmt.Errorf("error: %v", err))
		return
	}
	// rebuild repos
	err = rebuildRepos()
	if err != nil {
		respErr(c, fmt.Errorf("error: %v", err))
		return
	}

	respOK(c, nil)
}

func addRepo(c *gin.Context) {
	var newRepo *repo.Entry
	err := c.ShouldBindJSON(&newRepo)
	if err != nil && err != io.EOF {
		respErr(c, err)
		return
	}
	err = writeRepoConfig(c.Request.Method, newRepo)
	if err != nil && err != io.EOF {
		respErr(c, err)
		return
	}
	respOK(c, nil)
}

func deleteRepo(c *gin.Context) {
	name := c.Param("name")
	err := writeRepoConfig(c.Request.Method, &repo.Entry{
		Name: name,
	})
	if err != nil && err != io.EOF {
		respErr(c, err)
		return
	}
	respOK(c, nil)
}

func listRepos(c *gin.Context) {
	type repp struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	// read config
	_, err := readRepoConfig()
	if err != nil {
		glog.Errorln(err)
	}
	repos := []repp{}
	for _, r := range helmConfig.HelmRepos {
		repos = append(repos, repp{
			r.Name,
			r.URL,
		})
	}

	respOK(c, repos)
}

func listRepoCharts(c *gin.Context) {
	version := c.Query("version")   // chart version
	versions := c.Query("versions") // if "true", all versions
	keyword := c.Query("keyword")   // search keyword

	// default stable
	if version == "" {
		version = ">0.0.0"
	}

	index, err := buildSearchIndex(version)
	if err != nil {
		respErr(c, err)
		return
	}

	var res []*search.Result
	if keyword == "" {
		res = index.All()
	} else {
		res, err = index.Search(keyword, searchMaxScore, false)
		if err != nil {
			respErr(c, err)
			return
		}
	}

	search.SortScore(res)
	var versionsB bool
	if versions == "true" {
		versionsB = true
	}
	data, err := applyConstraint(version, versionsB, res)
	if err != nil {
		respErr(c, err)
		return
	}
	chartList := make(repoChartList, 0, len(data))
	for _, v := range data {
		chartList = append(chartList, repoChartElement{
			Name:        v.Name,
			Version:     v.Chart.Version,
			AppVersion:  v.Chart.AppVersion,
			Description: v.Chart.Description,
		})
	}

	respOK(c, chartList)
}

func SafeCloser(fileLock *flock.Flock, err *error) {
	if fileErr := fileLock.Unlock(); fileErr != nil && *err == nil {
		*err = fileErr
		glog.Error(fileErr)
	}
}
