package s3

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// PathMetrics struct
type PathMetrics struct {
	Path    string
	Size    uint64
	Objects uint64
}

// Metrics struct
type Metrics struct {
	PathMetrics   *[]PathMetrics
	BucketSize    uint64
	Objects       uint64
	StatsDuration float64
	StatsTimeout  float64
}

// Client struct
type Client struct {
	client         *minio.Client
	Bucket         string
	Endpoint       string
	MetricsTimeout time.Duration
	UseV1          bool
}

// NewFromEnv func
func NewFromEnv() *Client {
	c := Client{
		MetricsTimeout: 5 * time.Second,
		UseV1:          os.Getenv("S3_USE_V1") == "true",
	}

	// get credentials from env
	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	// get endpoint from restic repository url
	endpoint := strings.TrimPrefix(os.Getenv("RESTIC_REPOSITORY"), "s3:")

	// get bucket name
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	uri, err := url.Parse(endpoint)
	if err != nil {
		log.Fatalf("url.Parse: %v", err)
	}
	bucketRE := regexp.MustCompile("^/([^/]+)")
	match := bucketRE.FindStringSubmatch(uri.Path)
	if match == nil {
		log.Fatalf("bucketRE does not match")
	}
	c.Bucket = match[1]

	// detect SSL, strip protocol prefix
	useSSL := false
	if strings.HasPrefix(endpoint, "https://") {
		useSSL = true
		endpoint = strings.TrimPrefix(endpoint, "https://")
	}
	endpoint = strings.TrimPrefix(endpoint, "http://")

	// trim bucket and path from endpoint
	trimPathRE := regexp.MustCompile("/.*$")
	endpoint = trimPathRE.ReplaceAllString(endpoint, "")

	// new client
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalf("minio.New: %v", err)
	}
	c.client = client
	c.Endpoint = endpoint

	return &c
}

// ConnectTest func
func (c Client) ConnectTest() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.client.ListBuckets(ctx)
	return err
}

// GetMetrics func
func (c Client) GetMetrics() (Metrics, error) {

	ctx, cancel1 := context.WithTimeout(context.Background(), c.MetricsTimeout)
	defer cancel1()

	topDirs := []string{}
	if !c.looksLikeRepo("") {
		for object := range c.client.ListObjects(ctx, c.Bucket, minio.ListObjectsOptions{UseV1: c.UseV1}) {
			if object.Err != nil {
				return Metrics{}, fmt.Errorf("ListObjects: %v", object.Err)
			}
			if c.looksLikeRepo(object.Key) {
				topDirs = append(topDirs, object.Key)
			}
		}
	}
	cancel1()

	metrics := Metrics{}
	paths := []string{}
	pathMetrics := map[string]*PathMetrics{}
	pathMetricsEnabled := false
	if len(topDirs) > 0 {
		pathMetricsEnabled = true
		for _, path := range topDirs {
			p := strings.TrimPrefix(path, "/")
			p = strings.TrimSuffix(p, "/")
			pathMetrics[p] = &PathMetrics{Path: p}
			paths = append(paths, p)
		}
	}

	// set timeout for stats collection
	ctx, cancel2 := context.WithTimeout(context.Background(), c.MetricsTimeout)
	defer cancel2()

	t0 := time.Now()

	// loop over all objects
	for object := range c.client.ListObjects(ctx, c.Bucket, minio.ListObjectsOptions{Recursive: true, UseV1: c.UseV1}) {
		if object.Err != nil {
			if object.Err.Error() == "Truncated response should have continuation token set" {
				return Metrics{}, fmt.Errorf(
					"%v (hint: certain S3-compatible servers do not properly implement the "+
						"ListObjectsV2 API, most notably Ceph versions before v14.2.5. As "+
						"a temporary workaround, please set `S3_USE_V1=true`)",
					object.Err)
			}
			return Metrics{}, object.Err
		}

		// cature global metrics
		metrics.BucketSize += uint64(object.Size)
		metrics.Objects++

		// capture path metrics
		if pathMetricsEnabled {
			for _, prefix := range paths {
				if strings.HasPrefix(object.Key, prefix+"/") {
					pathMetrics[prefix].Size += uint64(object.Size)
					pathMetrics[prefix].Objects = pathMetrics[prefix].Objects + 1
					continue
				}
			}
		}
	}

	metrics.StatsDuration = time.Since(t0).Truncate(time.Millisecond).Seconds()
	metrics.StatsTimeout = c.MetricsTimeout.Seconds()

	if pathMetricsEnabled {
		pm := []PathMetrics{}
		for _, prefix := range paths {
			pm = append(pm, *pathMetrics[prefix])
		}
		metrics.PathMetrics = &pm
	}

	return metrics, nil
}

// PathExists checks if the given path exists
func (c Client) PathExists(path string) bool {
	path = strings.TrimLeft(path, "/")
	ctx, cancel := context.WithTimeout(context.Background(), c.MetricsTimeout)
	defer cancel()

	if strings.HasSuffix(path, "/") {
		for obj := range c.client.ListObjects(ctx, c.Bucket, minio.ListObjectsOptions{Prefix: path}) {
			return obj.Err == nil
		}
	} else {
		obj, err := c.client.StatObject(ctx, c.Bucket, path, minio.GetObjectOptions{})
		if err != nil {
			return false
		}
		return obj.ETag != ""
	}
	return false
}

func (c Client) looksLikeRepo(path string) bool {
	return c.PathExists(path+"config") && c.PathExists(path+"keys/")
}
