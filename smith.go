package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	ver  string
	sha  string
	opts options
)

func version() {
	if ver == "" {
		ver = "dev"
		sha = "unknown"
	}
	data := "smith version %s (built from sha %s)\n"
	fmt.Printf(data, ver, sha)
}

type options struct {
	verbose bool
	version bool
}

func main() {
	var buildOpts buildOptions
	var opts options
	var image string
	var cmdExitCode int
	defaultBuild := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	if host, err := os.Hostname(); err == nil {
		defaultBuild = defaultBuild + "-" + host
	}

	annotations := map[string][]string{
		cobra.BashCompFilenameExt: []string{"tar.gz"},
	}

	buildCmd := cobra.Command{
		Use:   "smith",
		Short: "smith - build hardened containers",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if opts.verbose {
				logrus.SetLevel(logrus.DebugLevel)
			} else {
				logrus.SetLevel(logrus.InfoLevel)
			}
		},
		Run: func(cmd *cobra.Command, args []string) {
			if opts.version {
				version()
				return
			}
			if len(args) != 0 {
				cmdExitCode = 1
				cmd.Usage()
				return
			}
			if !buildContainer(image, &buildOpts) {
				cmdExitCode = 1
			}
		},
	}
	f := buildCmd.Flags()
	f.BoolVarP(&buildOpts.fast, "fast", "f", false, "skip package install if possible")
	f.StringVarP(&buildOpts.conf, "conf", "c", "smith.yaml", "name of config file")
	f.StringVarP(&buildOpts.dir, "dir", "d", ".", "directory to build container image from")
	f.StringVarP(&image, "image", "i", "image.tar.gz", "container image file")
	f.StringVarP(&buildOpts.buildNo, "buildnumber", "b", defaultBuild, "unique build number")
	f.Lookup("image").Annotations = annotations
	f = buildCmd.PersistentFlags()
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "verbose output")
	f.BoolVarP(&opts.version, "version", "V", false, "show version")
	f.BoolVarP(&buildOpts.insecure, "insecure", "k", false, "skip tls verification")

	var remote string
	var docker bool
	uploadCmd := cobra.Command{
		Use:   "upload",
		Short: "upload oci to repository",
		Run: func(cmd *cobra.Command, args []string) {
			if opts.version {
				version()
				return
			}
			if len(args) != 0 {
				cmdExitCode = 1
				cmd.Usage()
				return
			}
			if !uploadContainer(image, remote, buildOpts.insecure, docker) {
				cmdExitCode = 1
			}
		},
	}
	f = uploadCmd.Flags()
	f.StringVarP(&image, "image", "i", "image.tar.gz", "container image file")
	f.StringVarP(&remote, "remote", "r", "", "remote repository path to upload to")
	f.BoolVarP(&docker, "docker", "d", false, "upload in docker format")
	buildCmd.AddCommand(&uploadCmd)

	downloadCmd := cobra.Command{
		Use:   "download",
		Short: "download oci to repository",
		Run: func(cmd *cobra.Command, args []string) {
			if opts.version {
				version()
				return
			}
			if len(args) != 0 {
				cmdExitCode = 1
				cmd.Usage()
				return
			}
			if !downloadContainer(image, remote, buildOpts.insecure) {
				cmdExitCode = 1
			}
		},
	}
	f = downloadCmd.Flags()
	f.StringVarP(&image, "image", "i", "image.tar.gz", "container image file")
	f.StringVarP(&remote, "remote", "r", "", "remote repository path to download from")
	buildCmd.AddCommand(&downloadCmd)

	buildCmd.Execute()
	os.Exit(cmdExitCode)
}
