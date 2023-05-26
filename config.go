package main

import (
  "context"
  "fmt"
  "github.com/gofrs/flock"
  "github.com/spf13/pflag"
  "helm.sh/helm/v3/pkg/repo"
  "io/ioutil"
  "net/http"
  "sigs.k8s.io/yaml"
  "time"
)

func readRepoConfig() (string, error) {
  configPath, _ := pflag.CommandLine.GetString("config")
  configBody, err := ioutil.ReadFile(configPath)
  if err != nil {
    return "", err
  }
  err = yaml.Unmarshal(configBody, helmConfig)
  if err != nil {
    return "", err
  }
  return configPath, nil
}

func writeRepoConfig(method string, helmRepo *repo.Entry) error {
  if method == http.MethodPost {
    for i := 0; i < len(helmConfig.HelmRepos); i++ {
      if helmConfig.HelmRepos[i].Name == helmRepo.Name {
        return fmt.Errorf("same name repo already exist: %s", helmRepo.Name)
      }
    }
    helmConfig.HelmRepos = append(helmConfig.HelmRepos, helmRepo)
  } else if method == http.MethodDelete {
    for i := 0; i < len(helmConfig.HelmRepos); i++ {
      if helmConfig.HelmRepos[i].Name == helmRepo.Name {
        helmConfig.HelmRepos = append(helmConfig.HelmRepos[:i], helmConfig.HelmRepos[i+1:]...)
      }
    }
  }

  configPath, _ := pflag.CommandLine.GetString("config")
  fileLock := flock.New(configPath)
  lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel()
  locked, err := fileLock.TryLockContext(lockCtx, time.Second)
  if err == nil && locked {
    SafeCloser(fileLock, &err)
  }
  if err != nil {
    return err
  }
  b, err := yaml.Marshal(helmConfig)
  if err := ioutil.WriteFile(configPath, b, 0644); err != nil {
    return err
  }

  // rebuild repos
  err = rebuildRepos()
  if err != nil {
    return err
  }
  return nil
}

func rebuildRepos() error {
  // clean all repo records
  fileLock := flock.New(settings.RepositoryConfig)
  lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel()
  locked, err := fileLock.TryLockContext(lockCtx, time.Second)
  if err == nil && locked {
    SafeCloser(fileLock, &err)
  }
  if err != nil {
    return err
  }
  var b []byte
  if err := ioutil.WriteFile(settings.RepositoryConfig, b, 0644); err != nil {
    return err
  }
  // rebuild repo records
  type errRepo struct {
    Name string
    Err  string
  }

  errRepoList := []errRepo{}

  for _, c := range helmConfig.HelmRepos {
    err := initRepos(c)
    if err != nil {
      errRepoList = append(errRepoList, errRepo{
        Name: c.Name,
        Err:  err.Error(),
      })
    }
  }
  if len(errRepoList) > 0 {
    return fmt.Errorf("error list: %v", errRepoList)
  }
  return nil
}
