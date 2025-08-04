package tar

import (
	"bytes"
	"carvel.dev/imgpkg/pkg/imgpkg/imagetar"
	"context"
	"errors"
	"fmt"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/base"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

const (
	driverName           = "tarball"
	defaultRootDirectory = "/var/lib/registry"
	defaultMaxThreads    = uint64(100)

	// minThreads is the minimum value for the maxthreads configuration
	// parameter. If the driver's parameters are less than this we set
	// the parameters to minThreads
	minThreads = uint64(25)
)

// DriverParameters represents all configuration options available for the
// filesystem driver
type DriverParameters struct {
	RootDirectory string
	MaxThreads    uint64
}

func init() {
	factory.Register(driverName, &tarDriverFactory{})
}

// filesystemDriverFactory implements the factory.StorageDriverFactory interface
type tarDriverFactory struct{}

func (factory *tarDriverFactory) Create(ctx context.Context, parameters map[string]interface{}) (storagedriver.StorageDriver, error) {
	return FromParameters(parameters)
}

type driver struct {
	rootDirectory string
	manifestMap   map[string][]byte
	blobDir       string
	tarPatch      string
	tarReader     imagetar.TarReader
}

type baseEmbed struct {
	base.Base
}

// Driver is a storagedriver.StorageDriver implementation backed by a local
// filesystem. All provided paths will be subpaths of the RootDirectory.
type Driver struct {
	baseEmbed
}

// FromParameters constructs a new Driver with a given parameters map
// Optional Parameters:
// - rootdirectory
// - maxthreads
func FromParameters(parameters map[string]interface{}) (*Driver, error) {
	params, err := fromParametersImpl(parameters)
	if err != nil || params == nil {
		return nil, err
	}
	return New(*params), nil
}

func fromParametersImpl(parameters map[string]interface{}) (*DriverParameters, error) {
	var (
		err           error
		maxThreads    = defaultMaxThreads
		rootDirectory = defaultRootDirectory
	)

	if parameters != nil {
		if rootDir, ok := parameters["rootdirectory"]; ok {
			rootDirectory = fmt.Sprint(rootDir)
		}

		maxThreads, err = base.GetLimitFromParameter(parameters["maxthreads"], minThreads, defaultMaxThreads)
		if err != nil {
			return nil, fmt.Errorf("maxthreads config error: %s", err.Error())
		}
	}

	params := &DriverParameters{
		RootDirectory: rootDirectory,
		MaxThreads:    maxThreads,
	}
	return params, nil
}

// New constructs a new Driver with a given rootDirectory
func New(params DriverParameters) *Driver {
	tarDriver := &driver{
		rootDirectory: params.RootDirectory,
		tarPatch:      params.RootDirectory + "harbor.tar",
	}

	return &Driver{
		baseEmbed: baseEmbed{
			Base: base.Base{
				StorageDriver: base.NewRegulator(tarDriver, params.MaxThreads),
			},
		},
	}
}

// Implement the storagedriver.StorageDriver interface

func (d *driver) Name() string {
	return driverName
}

// GetContent retrieves the content stored at "path" as a []byte.
func (d *driver) GetContent(ctx context.Context, path string) ([]byte, error) {
	reader := imagetar.NewTarReader(d.tarPatch)
	contents, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for _, image := range contents {
		if image.Image != nil {
			img := *image.Image
			imgDgst, err := img.Digest()
			if err != nil {
				return nil, err
			}
			mf, err := img.Manifest()
			if err != nil {
				return nil, err
			}
			if strings.Contains(path, strings.TrimPrefix(mf.Config.Digest.String(), "sha256:")) {
				return []byte(mf.Config.Digest.String()), nil
			}
			if strings.Contains(path, strings.TrimPrefix(imgDgst.String(), "sha256:")) {
				return []byte(imgDgst.String()), nil
			}
		}
	}
	layers, err := reader.PresentLayers()
	if err != nil {
		return nil, err
	}
	for _, layer := range layers {
		dgst, err := layer.Digest()
		if err != nil {
			return nil, err
		}
		if strings.Contains(path, strings.TrimPrefix(dgst.String(), "sha256:")) {
			return []byte(dgst.String()), nil
		}
	}
	return nil, fmt.Errorf("[GetContent] unable to locate the blob: %s", path)
}

// PutContent stores the []byte content at a location designated by "path".
func (d *driver) PutContent(ctx context.Context, subPath string, contents []byte) error {
	return errors.New("readonly driver")
}

// Reader retrieves an io.ReadCloser for the content stored at "path" with a
// given byte offset.
func (d *driver) Reader(ctx context.Context, path string, offset int64) (io.ReadCloser, error) {
	reader := imagetar.NewTarReader(d.tarPatch)
	contents, err := reader.Read()
	if err != nil {
		return nil, err
	}

	for _, image := range contents {
		if image.Image != nil {
			img := *image.Image
			imgDgst, err := img.Digest()
			if err != nil {
				return nil, err
			}
			mf, err := img.Manifest()
			if err != nil {
				return nil, err
			}
			if strings.Contains(path, strings.TrimPrefix(mf.Config.Digest.String(), "sha256:")) {
				rawConfig, err := img.RawConfigFile()
				if err != nil {
					return nil, err
				}
				return io.NopCloser(bytes.NewReader(rawConfig)), nil
			}
			if strings.Contains(path, strings.TrimPrefix(imgDgst.String(), "sha256:")) {
				rawMF, err := img.RawManifest()
				if err != nil {
					return nil, err
				}
				return io.NopCloser(bytes.NewReader(rawMF)), nil
			}
		}
	}

	layers, err := reader.PresentLayers()
	if err != nil {
		return nil, err
	}
	for _, layer := range layers {
		dgst, err := layer.Digest()
		if err != nil {
			return nil, err
		}
		if strings.Contains(path, strings.TrimPrefix(dgst.String(), "sha256:")) {
			return layer.Compressed()
		}
	}
	return nil, fmt.Errorf("[Reader] unable to locate the blob:%s", path)
}

func (d *driver) Writer(ctx context.Context, subPath string, append bool) (storagedriver.FileWriter, error) {
	return nil, errors.New("unsupported")
}

// Stat retrieves the FileInfo for the given path, including the current size
// in bytes and the creation time.
func (d *driver) Stat(ctx context.Context, subPath string) (storagedriver.FileInfo, error) {
	reader := imagetar.NewTarReader(d.tarPatch)
	contents, err := reader.Read()
	if err != nil {
		return nil, err
	}

	for _, image := range contents {
		if image.Image != nil {
			img := *image.Image
			imgDgst, err := img.Digest()
			if err != nil {
				return nil, err
			}
			mf, err := img.Manifest()
			if err != nil {
				return nil, err
			}
			if strings.Contains(subPath, strings.TrimPrefix(mf.Config.Digest.String(), "sha256:")) {
				return &staticFileInfo{size: mf.Config.Size, path: subPath}, nil
			}
			if strings.Contains(subPath, strings.TrimPrefix(imgDgst.String(), "sha256:")) {
				size, err := img.Size()
				if err != nil {
					return nil, err
				}
				return &staticFileInfo{size: size, path: subPath}, nil
			}
			layers, err := img.Layers()
			if err != nil {
				return nil, err
			}
			for _, layer := range layers {
				dgst, err := layer.Digest()
				if err != nil {
					return nil, err
				}
				if strings.Contains(subPath, strings.TrimPrefix(dgst.String(), "sha256:")) {
					size, err := layer.Size()
					if err != nil {
						return nil, err
					}
					return &staticFileInfo{size: size, path: subPath}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("[Stat] unable to locate the blob:%s", subPath)
}

// List returns a list of the objects that are direct descendants of the given
// path.
func (d *driver) List(ctx context.Context, subPath string) ([]string, error) {
	return nil, errors.New("readonly driver")
}

// Move moves an object stored at sourcePath to destPath, removing the original
// object.
func (d *driver) Move(ctx context.Context, sourcePath string, destPath string) error {
	return errors.New("readonly driver")
}

// Delete recursively deletes all objects stored at "path" and its subpaths.
func (d *driver) Delete(ctx context.Context, subPath string) error {
	return errors.New("readonly driver")
}

// RedirectURL returns a URL which may be used to retrieve the content stored at the given path.
func (d *driver) RedirectURL(*http.Request, string) (string, error) {
	return "", nil
}

// Walk traverses a filesystem defined within driver, starting
// from the given path, calling f on each file and directory
func (d *driver) Walk(ctx context.Context, path string, f storagedriver.WalkFn, options ...func(*storagedriver.WalkOptions)) error {
	return storagedriver.WalkFallback(ctx, d, path, f, options...)
}

// fullPath returns the absolute path of a key within the Driver's storage.
func (d *driver) fullPath(subPath string) string {
	return path.Join(d.rootDirectory, subPath)
}

type staticFileInfo struct {
	name string
	size int64
	path string
}

func (f *staticFileInfo) Name() string       { return f.name }
func (f *staticFileInfo) Size() int64        { return f.size }
func (f *staticFileInfo) ModTime() time.Time { return time.Now() }
func (f *staticFileInfo) IsDir() bool        { return false }
func (f *staticFileInfo) Mode() os.FileMode  { return 0444 }
func (f *staticFileInfo) Path() string       { return f.path }
func (f *staticFileInfo) Sys() interface{}   { return nil }
