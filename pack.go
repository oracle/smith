package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	gdigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	c_ISDIR          = 040000  // Directory
	c_ISREG          = 0100000 // Regular file
	c_ISLNK          = 0120000 // Symbolic link
	dockerLayerMT    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	dockerConfigMT   = "application/vnd.docker.container.image.v1+json"
	dockerManifestMT = "application/vnd.docker.distribution.manifest.v2+json"
	ociCreated       = "org.opencontainers.image.created"
	ociRefName       = "org.opencontainers.image.ref.name"
	layerMT          = v1.MediaTypeImageLayerGzip
	configMT         = v1.MediaTypeImageConfig
	manifestMT       = v1.MediaTypeImageManifest
	indexMT          = v1.MediaTypeImageIndex
	manifestVersion  = 2
)

func tarWriteFunc(baseDir string, tarOut *tar.Writer, uid int, gid int) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Base(path) == ".gitignore" {
			return nil
		}
		rpath, err := filepath.Rel(baseDir, path)
		if err != nil {
			logrus.Errorf("Failed to get relative path: %v", err)
			return err
		}
		if rpath == "." {
			return nil
		}
		header := new(tar.Header)
		header.Name = rpath
		header.Uid = uid
		header.Gid = gid
		header.Mode = int64(info.Mode().Perm())
		header.ModTime = time.Time{}
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode |= c_ISDIR
			header.Name += string(filepath.Separator)
			logrus.Debugf("Adding directory %v to archive", path)
			err = tarOut.WriteHeader(header)
			if err != nil {
				return err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				logrus.Errorf("Symlink cannot be read: %v", err)
				return err
			}
			header.Typeflag = tar.TypeSymlink
			header.Mode |= c_ISLNK
			header.Linkname = link
			logrus.Debugf("Adding symlink %v to archive", path)
			err = tarOut.WriteHeader(header)
			if err != nil {
				return err
			}
		} else {
			header.Typeflag = tar.TypeReg
			header.Mode |= c_ISREG
			header.Size = info.Size()
			logrus.Debugf("Adding file %v to archive", path)
			err = tarOut.WriteHeader(header)
			if err != nil {
				return err
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tarOut, file)
			if err != nil {
				file.Close()
				return err
			}
			file.Close()
		}
		return nil
	}
}

func writeDirTar(tarOut *tar.Writer, path string) error {
	// basic header
	header := new(tar.Header)
	header.ModTime = time.Time{}
	header.Typeflag = tar.TypeDir
	header.Mode = 0755 | c_ISDIR
	header.Name = path
	logrus.Debugf("Adding directory %v to archive", path)
	if err := tarOut.WriteHeader(header); err != nil {
		return err
	}
	return nil
}

func writeFileTar(tarOut *tar.Writer, path string, b []byte) error {
	// basic header
	header := new(tar.Header)
	header.ModTime = time.Time{}
	header.Typeflag = tar.TypeReg
	header.Mode = 0644 | c_ISREG
	header.Size = int64(len(b))
	header.Name = path
	logrus.Debugf("Adding file %v to archive", path)
	if err := tarOut.WriteHeader(header); err != nil {
		return err
	}
	if _, err := io.Copy(tarOut, bytes.NewReader(b)); err != nil {
		return err
	}
	return nil
}

func configFromDef(def *ConfigDef) *v1.Image {
	config := v1.Image{}
	config.Architecture = "amd64"
	config.OS = "linux"
	config.Config.Entrypoint = def.Entrypoint
	config.Config.Cmd = def.Cmd
	config.Config.Env = def.Env
	config.Config.WorkingDir = def.Dir
	config.Config.ExposedPorts = def.Ports
	if def.Root {
		config.Config.User = "0:0"
	} else if def.User != "" {
		config.Config.User = def.User
	} else {
		config.Config.User = fmt.Sprintf("%d:%d", DefaultID, DefaultID)
	}
	for _, vol := range def.Mounts {
		config.Config.Volumes[vol] = struct{}{}
	}
	return &config
}

func serializeConfig(image *Image) ([]byte, error) {
	config := image.Config
	// reset rootfs section so it can be regenerated
	config.RootFS = v1.RootFS{Type: "layers"}
	for _, l := range image.Layers {
		config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, l.DiffID)
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return data, nil
}

type maybeDockerManifest struct {
	v1.Manifest
	MediaType string `json:"mediaType,omitempty"`
}

func serializeManifest(conf v1.Descriptor, layers []*Layer, docker bool) ([]byte, error) {
	manifest := maybeDockerManifest{}
	manifest.SchemaVersion = manifestVersion
	if docker {
		manifest.MediaType = dockerManifestMT
	}

	manifest.Config = conf
	if docker {
		manifest.Config.MediaType = dockerConfigMT
	} else {
		manifest.Config.MediaType = configMT
	}
	for _, l := range layers {
		desc := l.Desc
		if docker {
			desc.MediaType = dockerLayerMT
		} else {
			desc.MediaType = layerMT
		}
		manifest.Layers = append(manifest.Layers, desc)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func digest(b []byte) gdigest.Digest {
	return gdigest.FromBytes(b)
}

func desc(mt string, b []byte, digest gdigest.Digest) v1.Descriptor {
	return v1.Descriptor{
		MediaType: mt,
		Digest:    digest,
		Size:      int64(len(b)),
	}
}

func imageIndex(entries []v1.Descriptor, annotations map[string]string) v1.Index {
	rv := v1.Index{Manifests: entries}
	rv.SchemaVersion = manifestVersion
	return rv
}

func layerFromPath(path string, uid int, gid int) (*Layer, error) {
	b := bytes.Buffer{}
	gzipHash := sha256.New()
	tarHash := sha256.New()
	gzipOut, err := MaybeGzipWriter(io.MultiWriter(&b, gzipHash))
	if err != nil {
		return nil, err
	}
	tarOut := tar.NewWriter(io.MultiWriter(gzipOut, tarHash))
	writeTarFile := tarWriteFunc(path, tarOut, uid, gid)
	if err := filepath.Walk(path, writeTarFile); err != nil {
		logrus.Errorf("Failed to walk directory %v: %v", path, err)
		tarOut.Close()
		gzipOut.Close()
		return nil, err
	}
	// explicitly close the gzip so we wait for the write to complete
	tarOut.Close()
	gzipOut.Close()
	diffSha := gdigest.NewDigest("sha256", tarHash)
	logrus.Infof("DiffID of layer is %s", diffSha)
	layerSha := gdigest.NewDigest("sha256", gzipHash)
	layer := Layer{DiffID: diffSha, Data: b.Bytes()}
	layer.Desc = desc(layerMT, layer.Data, layerSha)
	return &layer, nil
}

func extractFile(tarfile, filename string) ([]byte, error) {
	in, err := os.OpenFile(tarfile, os.O_RDONLY, 0)
	if err != nil {
		logrus.Errorf("Failed to open %v: %v", tarfile, err)
		return nil, err
	}
	defer in.Close()
	gzipIn, err := MaybeGzipReader(in)
	if err != nil {
		logrus.Errorf("Failed to read gzip: %v", err)
		return nil, err
	}
	defer gzipIn.Close()
	tarIn := tar.NewReader(gzipIn)
	for {
		hdr, err := tarIn.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Error reading tar entry: %v", err)
		}
		clean := filepath.Clean(hdr.Name)
		if clean == filename {
			// found the reference we are supposed to use
			return ioutil.ReadAll(tarIn)
		}
	}
	return nil, fmt.Errorf("Could not find %s in %s", filename, tarfile)
}

func digestExtractor(tarfile string) Extractor {
	return func(digest gdigest.Digest) ([]byte, error) {
		parts := append([]string{"blobs"},
			string(digest.Algorithm()),
			digest.Hex())
		filename := filepath.Join(parts...)
		return extractFile(tarfile, filename)
	}
}

type Extractor func(digest gdigest.Digest) ([]byte, error)

func imageFromDigest(extract Extractor, digest gdigest.Digest, annotations map[string]string) (*Image, error) {
	manb, err := extract(digest)
	if err != nil {
		return nil, err
	}
	var manifest v1.Manifest
	if err := json.Unmarshal(manb, &manifest); err != nil {
		return nil, fmt.Errorf("error unmarshaling image manifest")
	}
	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("manifest has no referenced config")
	}
	configb, err := extract(manifest.Config.Digest)
	if err != nil {
		return nil, err
	}
	var config v1.Image
	if err := json.Unmarshal(configb, &config); err != nil {
		return nil, fmt.Errorf("error unmarshaling image config json")
	}
	if len(manifest.Layers) != len(config.RootFS.DiffIDs) {
		return nil, fmt.Errorf("number of layers and number of diffIDs don't match")
	}
	// smith doesn't set created in the config so that the SHA of the config
	// is deterministic, but other tools use the created value. Set Created from
	// the annotations when unpacking to make registries happy upon upload.
	if config.Created == nil {
		strval := manifest.Annotations[ociCreated]
		if strval == "" {
			strval = annotations[ociCreated]
		}
		if created, err := time.Parse(time.RFC3339, strval); err == nil {
			config.Created = &created
		}
	}
	layers := []*Layer{}
	for i := range manifest.Layers {
		layer := Layer{}
		layer.Desc = manifest.Layers[i]
		layer.DiffID = config.RootFS.DiffIDs[i]
		if layer.Desc.Digest == "" {
			return nil, fmt.Errorf("image config has an invalid layer reference")
		}
		layer.Data, err = extract(layer.Desc.Digest)
		if err != nil {
			return nil, err
		}
		layers = append(layers, &layer)
	}
	return &Image{Config: &config, Layers: layers}, nil
}

type Layer struct {
	Desc   v1.Descriptor
	DiffID gdigest.Digest
	Data   []byte
}

// ImageMetadata stores metadata such as build time
type ImageMetadata struct {
	Buildno   string
	BuildHost string
	BuildTime time.Time
	SmithVer  string
	SmithSha  string
}

type Image struct {
	Config          *v1.Image
	Layers          []*Layer
	AdditionalBlobs []OpaqueBlob
	Metadata        *ImageMetadata
}

// OpaqueBlob adds data other than image layers
type OpaqueBlob struct {
	Filetype string
	Content  []byte
}

func imageFromFile(path string) (*Image, error) {
	tag := "latest"
	parts := strings.Split(path, ":")
	tarpath := parts[0]
	if len(parts) > 1 {
		tag = parts[1]
	}
	refb, err := extractFile(tarpath, "index.json")
	if err != nil {
		return nil, err
	}
	var ref v1.Index
	if err := json.Unmarshal(refb, &ref); err != nil {
		return nil, fmt.Errorf("error unmarshaling index.json from %s", tarpath)
	}
	digest := gdigest.Digest("")
	annotations := map[string]string{}
	for _, defn := range ref.Manifests {
		if defn.Annotations[ociRefName] == tag {
			digest = defn.Digest
			annotations = defn.Annotations
		}
	}
	if digest == "" {
		return nil, fmt.Errorf("unable to locate image named %s in index", tag)
	}
	logrus.Debugf("%s in %s is id %s", tag, path, digest)
	return imageFromDigest(digestExtractor(tarpath), digest, annotations)
}

func setDefaultsFromImage(def *ConfigDef, image *Image) {
	if def.Dir == "" {
		def.Dir = image.Config.Config.WorkingDir
	}
	if len(def.Entrypoint) == 0 {
		def.Entrypoint = image.Config.Config.Entrypoint
	}
	if len(def.Cmd) == 0 {
		def.Cmd = image.Config.Config.Cmd
	}
	if len(def.Env) == 0 {
		def.Env = image.Config.Config.Env
	}
	if len(def.Ports) == 0 {
		def.Ports = image.Config.Config.ExposedPorts
	}
}

func imageFromBuild(def *ConfigDef, baseDir string) (*Image, error) {
	// get parent layers
	image := &Image{}
	if def.Parent != "" {
		var err error
		image, err = imageFromFile(filepath.Join(baseDir, def.Parent))
		if err != nil {
			return nil, err
		}
		setDefaultsFromImage(def, image)
	}
	image.Config = configFromDef(def)
	uid, gid, _, _, _ := ParseUser(def.User)
	layer, err := layerFromPath(filepath.Join(baseDir, rootfs), uid, gid)
	if err != nil {
		return nil, err
	}
	found := false
	for _, l := range image.Layers {
		if l.DiffID == layer.DiffID {
			found = true
			logrus.Infof("Layer with DiffID %s already exists in parent", l.DiffID)
		}
	}
	if !found {
		image.Layers = append(image.Layers, layer)
	}
	return image, nil
}

func WriteOciFromBuild(def *ConfigDef, buildDir, outName string, metadata *ImageMetadata, blobs []OpaqueBlob) error {
	image, err := imageFromBuild(def, buildDir)
	if err != nil {
		return err
	}
	image.AdditionalBlobs = blobs
	if metadata != nil {
		image.Metadata = metadata
	}
	if err := WriteOciTarGz(image, outName); err != nil {
		return err
	}
	return nil
}

func WriteOciTarGz(image *Image, outName string) error {
	out, err := os.OpenFile(outName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	gzipOut, err := MaybeGzipWriter(out)
	if err != nil {
		return err
	}
	if err := WriteOciTar(image, gzipOut); err != nil {
		logrus.Errorf("Error writing oci tar.gz: %v", err)
		gzipOut.Close()
		return err
	}
	// explicitly close the gzip so we wait for the write to complete
	gzipOut.Close()
	return nil
}

// WriteOciTar makes a tar file from in-memory structures
func WriteOciTar(image *Image, out io.Writer) error {
	tarOut := tar.NewWriter(out)
	defer tarOut.Close()

	fileData := map[string][]byte{}

	// add layers
	for _, l := range image.Layers {
		digest := l.Desc.Digest
		parts := append([]string{"blobs"}, string(digest.Algorithm()), digest.Hex())
		logrus.Infof("Adding layer %s to image", l.Desc.Digest)
		fileData[filepath.Join(parts...)] = l.Data
	}

	// add config
	shaBase := filepath.Join("blobs", "sha256")
	configData, err := serializeConfig(image)
	if err != nil {
		return err
	}
	configSha := digest(configData)
	fileData[filepath.Join(shaBase, configSha.Hex())] = configData
	configDesc := desc(configMT, configData, configSha)

	// add manifest
	manifestData, err := serializeManifest(configDesc, image.Layers, false)
	manifestSha := digest(manifestData)
	fileData[filepath.Join(shaBase, manifestSha.Hex())] = manifestData

	// add extra blobs
	for _, b := range image.AdditionalBlobs {
		d := digest(b.Content)
		fileData[filepath.Join(shaBase, d.Hex())] = b.Content
	}

	// write file blobs in sorted order
	filenames := make([]string, len(fileData))
	i := 0
	for k := range fileData {
		filenames[i] = k
		i++
	}
	sort.Strings(filenames)
	writeDirTar(tarOut, "blobs")
	dirs := map[string]struct{}{}
	for _, filename := range filenames {
		dir := filepath.Dir(filename)
		if _, ok := dirs[dir]; !ok {
			writeDirTar(tarOut, dir)
			dirs[dir] = struct{}{}
		}
		writeFileTar(tarOut, filename, fileData[filename])
	}

	layout := v1.ImageLayout{Version: "1.0.0"}
	layoutData, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	writeFileTar(tarOut, "oci-layout", layoutData)

	// build manifest entry for "latest" image manifest
	latest := desc(manifestMT, manifestData, manifestSha)
	if image.Metadata != nil {
		latest.Annotations = map[string]string{}
		latest.Annotations[ociRefName] = "latest"
		created := image.Metadata.BuildTime.Format(time.RFC3339)
		latest.Annotations[ociCreated] = created
		latest.Annotations["com.oracle.smith.version"] = image.Metadata.SmithVer
		latest.Annotations["com.oracle.smith.sha"] = image.Metadata.SmithSha
		if image.Metadata.Buildno != "" {
			latest.Annotations["com.oracle.smith.build"] = image.Metadata.Buildno
		}
	}
	platform := v1.Platform{Architecture: runtime.GOARCH, OS: runtime.GOOS}
	latest.Platform = &platform
	allBlobs := make([]v1.Descriptor, len(image.AdditionalBlobs)+1)

	// build entries for the rest of the blobs
	annotations := map[string]string{}
	if image.Metadata != nil && image.Metadata.Buildno != "" {
		annotations["com.oracle.smith.build"] = image.Metadata.Buildno
	}
	for i, b := range image.AdditionalBlobs {
		d := digest(b.Content)
		entry := desc(b.Filetype, b.Content, d)
		entry.Annotations = annotations
		allBlobs[i] = entry
	}
	allBlobs[len(image.AdditionalBlobs)] = latest
	index := imageIndex(allBlobs, nil)
	indexData, err := json.Marshal(index)
	if err != nil {
		return err
	}
	writeFileTar(tarOut, "index.json", indexData)
	return nil
}

func writeFile(path string, in io.Reader, perm os.FileMode) error {
	out, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		logrus.Errorf("Failed to open %v: %v", path, err)
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	if err != nil {
		logrus.Errorf("Failed to copy bits to %v: %v", path, err)
		return err
	}
	return nil
}

func extractLayer(layer *Layer, outDir string) error {
	gzipIn, err := MaybeGzipReader(NopCloser(bytes.NewReader(layer.Data)))
	if err != nil {
		logrus.Errorf("Failed to read gzip: %v", err)
		return err
	}
	defer gzipIn.Close()
	tarIn := tar.NewReader(gzipIn)
	for {
		hdr, err := tarIn.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return fmt.Errorf("Error reading tar entry: %v", err)
		}
		// extract tar file
		clean := filepath.Clean(hdr.Name)
		base := filepath.Base(clean)
		if strings.HasPrefix(base, ".wh.") {
			filename := filepath.Join(filepath.Dir(clean), base[4:])
			if err := os.RemoveAll(filepath.Join(outDir, filename)); err != nil {
				logrus.Warnf("Failed to remove whiteout %s", filename)
			}
			continue
		}

		path := filepath.Join(outDir, clean)
		info, err := os.Lstat(path)
		// remove any existing file at the location unless both locations are a dir
		if err == nil && !(hdr.Typeflag == tar.TypeDir && info.IsDir()) {
			if err := os.RemoveAll(path); err != nil {
				logrus.Warnf("Failed to remove %s", path)
			}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(path, 0755)
		case tar.TypeSymlink:
			os.Symlink(hdr.Linkname, path)
		case tar.TypeLink:
			target := hdr.Linkname
			if filepath.IsAbs(target) {
				target = filepath.Join(outDir, target)
			}
			os.Link(target, path)
		case tar.TypeReg:
			// normalize permissions
			perm := int64(0644)
			if hdr.FileInfo().Mode().Perm()&0100 != 0 {
				perm = int64(0755)
			}
			hdr.Mode = perm | c_ISREG
			fPerm := hdr.FileInfo().Mode().Perm()
			if err := writeFile(path, tarIn, fPerm); err != nil {
				return err
			}
		default:
			logrus.Infof("Skipping unknown file type %v for %s", hdr.Typeflag, clean)
		}
	}
	return nil
}

func ExtractOci(image *Image, outDir string) error {
	// extract layers
	for _, layer := range image.Layers {
		if err := extractLayer(layer, outDir); err != nil {
			return err
		}
	}
	return nil
}
