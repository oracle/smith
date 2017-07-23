package main

import (
	"io/ioutil"

	"github.com/Sirupsen/logrus"
	"github.com/ghodss/yaml"
)

type MockDef struct {
	Config     string   `json:"config,omitempty"`
	PreBuild   string   `json:"pre-build,omitempty"`
	PostBuild  string   `json:"post-build,omitempty"`
	Deps       []string `json:"deps,omitempty"`
	DebugInfo  bool     `json:"debuginfo,omitempty"`
	DebugDeps  []string `json:"debugdeps,omitempty"`
	DebugPaths []string `json:"debugpaths,omitempty"`
}

type ConfigDef struct {
	Type       string              `json:"type,omitempty"` //defaults to "mock"
	Mock       MockDef             `json:"mock,omitempty"`
	Package    string              `json:"package,omitempty"`
	Paths      []string            `json:"paths,omitempty"`
	Excludes   []string            `json:"excludes,omitempty"`
	Parent     string              `json:"parent,omitempty"`
	Nss        bool                `json:"nss,omitempty"`
	Root       bool                `json:"root,omitempty"`
	User       string              `json:"user,omitempty"`
	Mounts     []string            `json:"mounts,omitempty"`
	Entrypoint []string            `json:"entrypoint,omitempty"`
	Cmd        []string            `json:"cmd,omitempty"`
	Dir        string              `json:"dir,omitempty"`
	Env        []string            `json:"env,omitempty"`
	Ports      map[string]struct{} `json:"ports,omitempty"`
}

func ReadConfig(path string) (*ConfigDef, error) {
	ydef, err := ioutil.ReadFile(path)
	if err != nil {
		logrus.Errorf("Failed to read %v: %v", path, err)
		return nil, err
	}
	var def ConfigDef
	err = yaml.Unmarshal(ydef, &def)
	if err != nil {
		logrus.Errorf("Failed to unmarshall %v: %v", path, err)
		return nil, err
	}
	return &def, nil
}

func (n *ConfigDef) WriteConfig(path string) error {
	data, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	logrus.Debugf("Writing normalized config to %v", path)
	return ioutil.WriteFile(path, data, 0644)
}
