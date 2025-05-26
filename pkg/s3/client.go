// Copyright 2018 jsonnet-bundler authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package s3

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pkg/errors"
)

// Client represents an S3 client for interacting with remote caches
type Client struct {
	Endpoint   string
	Bucket     string
	AccessKey  string
	SecretKey  string
	Region     string
	SSLMode    bool
	PathStyle  bool
	awsConfig  aws.Config
	s3Client   *s3.Client
	uploader   *manager.Uploader
	downloader *manager.Downloader
}

// IsS3URL checks if a URL is an S3 URL
func IsS3URL(url string) bool {
	return strings.HasPrefix(url, "s3://")
}

// SaveObjectToFile downloads an S3 object and saves it to a file
func SaveObjectToFile(s3URL, filePath string, quiet bool) error {
	// if !quiet {
	// 	fmt.Printf("S3 Client Debug - Attempting to download from S3 URL: %s to %s\n", s3URL, filePath)
	// }

	// Extract the key and bucket from the URL
	u, err := url.Parse(s3URL)
	if err != nil {
		return fmt.Errorf("failed to parse S3 URL: %w", err)
	}

	// Check if it's an S3 URL
	if u.Scheme != "s3" {
		return fmt.Errorf("URL is not an S3 URL (should start with s3://)")
	}

	// Extract bucket and key
	bucket := u.Host
	key := u.Path

	// if !quiet {
	// 	fmt.Printf("S3 Client Debug - Extracted bucket: '%s', key: '%s'\n", bucket, key)
	// }

	key = FormatKey(key)
	// if !quiet {
	// 	fmt.Printf("S3 Client Debug - Formatted key: '%s'\n", key)
	// }

	// Create client using environment variables
	client, err := NewClientFromEnv(bucket)
	if err != nil {
		return fmt.Errorf("failed to create client from environment: %w", err)
	}

	// Create context for the operation
	ctx := context.Background()

	// Check if bucket exists and is accessible
	exists, err := client.BucketExists(ctx)
	if err != nil {
		return fmt.Errorf("failed to check if bucket exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket '%s' does not exist or is not accessible", client.Bucket)
	}

	// Download the object
	err = client.Download(ctx, key, filePath)
	if err != nil {
		return fmt.Errorf("failed to download file from S3: %w", err)
	}

	// if !quiet {
	// 	fmt.Printf("S3 Client Debug - Successfully downloaded from S3 to %s\n", filePath)
	// }
	return nil
}

// NewClientFromURL parses an S3 URL and creates a new client
func NewClientFromURL(s3URL string) (*Client, error) {
	// Parse the URL to extract endpoint, bucket, etc.
	u, err := url.Parse(s3URL)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 URL: %v", err)
	}

	// Check if it's an S3 URL
	if u.Scheme != "s3" {
		return nil, fmt.Errorf("URL is not an S3 URL (should start with s3://)")
	}

	// Extract bucket from host
	bucket := u.Host

	// Create client config prioritizing environment variables first
	// then falling back to URL parameters

	// 1. Start with environment variables
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	endpoint := os.Getenv("AWS_ENDPOINT")
	region := os.Getenv("AWS_REGION")
	// Don't default to us-east-1, let AWS SDK detect the region

	// Path style defaults to false unless env var is set
	pathStyle := false
	if val := os.Getenv("AWS_S3_FORCE_PATH_STYLE"); val == "true" {
		pathStyle = true
		// fmt.Printf("S3 Client Debug - Using path style from environment var\n")
	}

	// Determine SSL mode from environment endpoint if present
	sslMode := true
	if strings.HasPrefix(endpoint, "http://") {
		sslMode = false
	}

	// 2. Extract query params if present and override environment variables if not set
	q := u.Query()

	// Override with URL parameters if provided and env vars not set
	if urlEndpoint := q.Get("endpoint"); urlEndpoint != "" {
		endpoint = urlEndpoint
		// fmt.Printf("S3 Client Debug - Using endpoint from URL: '%s'\n", endpoint)
		// Update SSL mode based on URL endpoint
		if strings.HasPrefix(endpoint, "http://") {
			sslMode = false
		}
	}

	if urlRegion := q.Get("region"); urlRegion != "" {
		region = urlRegion
		// fmt.Printf("S3 Client Debug - Using region from URL: '%s'\n", region)
	}

	if q.Get("ssl") == "false" {
		sslMode = false
		// fmt.Printf("S3 Client Debug - SSL disabled from URL parameter\n")
	} else if q.Get("ssl") == "true" {
		sslMode = true
		// fmt.Printf("S3 Client Debug - SSL enabled from URL parameter\n")
	}

	if q.Get("path_style") == "true" {
		pathStyle = true
		// fmt.Printf("S3 Client Debug - Path style enabled from URL parameter\n")
	} else if q.Get("path_style") == "false" && !strings.Contains(endpoint, "localhost") && !strings.Contains(endpoint, "127.0.0.1") {
		pathStyle = false
		// fmt.Printf("S3 Client Debug - Path style disabled from URL parameter\n")
	}

	// Use URL access keys only if environment variables are not set
	if accessKey == "" {
		accessKey = q.Get("access_key")
		if accessKey != "" {
			// fmt.Printf("S3 Client Debug - Using access key from URL\n")
		}
	}

	if secretKey == "" {
		secretKey = q.Get("secret_key")
		if secretKey != "" {
			// fmt.Printf("S3 Client Debug - Using secret key from URL\n")
		}
	}

	// Always force path style for localhost/127.0.0.1 endpoints
	if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
		pathStyle = true
		// fmt.Printf("S3 Client Debug - LocalStack detected, forcing path style access\n")
	}

	client, err := NewClient(endpoint, bucket, accessKey, secretKey, region, sslMode, pathStyle)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	// // Debug S3 client configuration
	// fmt.Printf("S3 Client Debug - Creating client with configuration:\n")
	// fmt.Printf("  Bucket: %s\n", bucket)
	// fmt.Printf("  Endpoint: %s\n", endpoint)
	// fmt.Printf("  Region: %s\n", region)
	// fmt.Printf("  Path Style: %v\n", pathStyle)
	// fmt.Printf("  SSL Mode: %v\n", sslMode)
	// fmt.Printf("  Has Access Key: %v\n", accessKey != "")
	// fmt.Printf("  Has Secret Key: %v\n", secretKey != "")

	// Initialize AWS config and client
	err = client.initialize(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize S3 client: %w (bucket=%s, endpoint=%s)",
			err, bucket, endpoint)
	}

	return client, nil
}

// NewClient creates a new S3 client with the provided parameters
// Environment variables are still checked first, and explicit parameters override them
func NewClient(endpoint, bucket, accessKey, secretKey, region string, sslMode, pathStyle bool) (*Client, error) {
	// Check environment variables first for any values not explicitly provided
	if accessKey == "" {
		if envKey := os.Getenv("AWS_ACCESS_KEY_ID"); envKey != "" {
			accessKey = envKey
			// fmt.Printf("S3 Client Debug - Using access key from environment\n")
		}
	}

	if secretKey == "" {
		if envSecret := os.Getenv("AWS_SECRET_ACCESS_KEY"); envSecret != "" {
			secretKey = envSecret
			// fmt.Printf("S3 Client Debug - Using secret key from environment\n")
		}
	}

	if region == "" {
		if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
			region = envRegion
			// fmt.Printf("S3 Client Debug - Using region from environment: %s\n", region)
		}
		// Don't default to us-east-1, let AWS SDK detect the region from config/metadata
	}

	// Only use environment endpoint if no explicit endpoint was provided
	if endpoint == "" {
		if envEndpoint := os.Getenv("AWS_ENDPOINT"); envEndpoint != "" {
			endpoint = envEndpoint
			// fmt.Printf("S3 Client Debug - Using endpoint from environment: %s\n", endpoint)

			// Update SSL mode based on endpoint if not explicitly set
			if strings.HasPrefix(endpoint, "http://") {
				sslMode = false
			}
		}
	}

	// Check for path style in environment if not explicitly set
	if !pathStyle {
		if val := os.Getenv("AWS_S3_FORCE_PATH_STYLE"); val == "true" {
			pathStyle = true
			// fmt.Printf("S3 Client Debug - Using path style from environment\n")
		}
	}

	// Always force path style for localhost/127.0.0.1 endpoints
	if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
		pathStyle = true
		// fmt.Printf("S3 Client Debug - LocalStack detected, forcing path style access\n")
	}

	client := &Client{
		Endpoint:  endpoint,
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    region,
		SSLMode:   sslMode,
		PathStyle: pathStyle,
	}

	// // Debug S3 client configuration
	// fmt.Printf("S3 Client Debug - Creating client with configuration:\n")
	// fmt.Printf("  Bucket: %s\n", bucket)
	// fmt.Printf("  Endpoint: %s\n", endpoint)
	// fmt.Printf("  Region: %s\n", region)
	// fmt.Printf("  Path Style: %v\n", pathStyle)
	// fmt.Printf("  SSL Mode: %v\n", sslMode)
	// fmt.Printf("  Has Access Key: %v\n", accessKey != "")
	// fmt.Printf("  Has Secret Key: %v\n", secretKey != "")

	// Initialize AWS config and client
	err := client.initialize(context.Background())
	if err != nil {
		return nil, err
	}

	return client, nil
}

// NewClientFromEnv creates a new S3 client using environment variables
// This is now a simplified wrapper around NewClient that explicitly uses env vars
func NewClientFromEnv(bucket string) (*Client, error) {
	// Read environment variables
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	endpoint := os.Getenv("AWS_ENDPOINT")
	region := os.Getenv("AWS_REGION")
	// Don't default to us-east-1, let AWS SDK detect the region

	// Path style is determined from environment
	pathStyle := false
	if val := os.Getenv("AWS_S3_FORCE_PATH_STYLE"); val == "true" {
		pathStyle = true
	}

	// Force path style for LocalStack
	if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
		pathStyle = true
		// fmt.Printf("S3 Client Debug - LocalStack detected, forcing path style access\n")
	}

	// Determine SSL mode based on the endpoint
	sslMode := true
	if strings.HasPrefix(endpoint, "http://") {
		sslMode = false
	}

	// // Debug output env vars
	// fmt.Printf("S3 Client Debug - Creating client from environment variables:\n")
	// fmt.Printf("  Bucket: %s\n", bucket)
	// fmt.Printf("  Endpoint: %s\n", endpoint)
	// fmt.Printf("  Region: %s\n", region)
	// fmt.Printf("  Path Style: %v\n", pathStyle)
	// fmt.Printf("  SSL Mode: %v\n", sslMode)
	// fmt.Printf("  Has Access Key: %v\n", accessKey != "")
	// fmt.Printf("  Has Secret Key: %v\n", secretKey != "")

	// Use the more general NewClient function, which will also check env vars
	return NewClient(endpoint, bucket, accessKey, secretKey, region, sslMode, pathStyle)
}

// initialize sets up AWS config and S3 client
func (c *Client) initialize(ctx context.Context) error {
	// Debug output for endpoint information
	// fmt.Printf("S3 Client Debug - Endpoint: '%s', Region: '%s', PathStyle: %v\n", c.Endpoint, c.Region, c.PathStyle)

	// Ensure we have a valid URL format for the endpoint upfront
	if c.Endpoint != "" && !strings.HasPrefix(c.Endpoint, "http://") && !strings.HasPrefix(c.Endpoint, "https://") {
		// Add http:// prefix if missing and not using SSL
		if !c.SSLMode {
			c.Endpoint = "http://" + c.Endpoint
			// fmt.Printf("S3 Client Debug - Added http:// prefix to endpoint: '%s'\n", c.Endpoint)
		} else {
			c.Endpoint = "https://" + c.Endpoint
			// fmt.Printf("S3 Client Debug - Added https:// prefix to endpoint: '%s'\n", c.Endpoint)
		}
	}

	// Load config using options
	var configOpts []func(*config.LoadOptions) error

	// Only use custom endpoint resolver if an endpoint is provided (for localstack, etc.)
	if c.Endpoint != "" {
		// Create custom resolver for endpoint
		// This is critical for making localstack work with AWS SDK v2
		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			// Always return the custom endpoint for any service in any region
			// This is necessary for localstack compatibility
			signingRegion := c.Region
			if signingRegion == "" {
				// Use the region passed by the SDK if we don't have one
				signingRegion = region
			}
			endpoint := aws.Endpoint{
				URL:               c.Endpoint,
				SigningRegion:     signingRegion,
				HostnameImmutable: true,
			}
			// fmt.Printf("S3 Client Debug - Resolving endpoint to URL: '%s', SigningRegion: '%s'\n",
			// endpoint.URL, endpoint.SigningRegion)
			return endpoint, nil
		})

		// fmt.Printf("S3 Client Debug - Using custom endpoint: %s\n", c.Endpoint)
		configOpts = append(configOpts, config.WithEndpointResolverWithOptions(customResolver))

		// Disable AWS SDK v2's automatic endpoint resolution
		configOpts = append(configOpts, config.WithRetryMaxAttempts(1))
	}

	// Add region if explicitly provided
	if c.Region != "" {
		configOpts = append(configOpts, config.WithRegion(c.Region))
	}
	// If no region provided, LoadDefaultConfig will use the default region chain:
	// 1. AWS_REGION environment variable
	// 2. AWS_DEFAULT_REGION environment variable  
	// 3. Region from shared config file (~/.aws/config)
	// 4. EC2 instance metadata service (if on EC2)

	// Add credentials if provided, otherwise use default AWS credential chain
	if c.AccessKey != "" && c.SecretKey != "" {
		configOpts = append(configOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""),
		))
	}
	// If no credentials provided, LoadDefaultConfig will use the default credential chain:
	// 1. Environment variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN)
	// 2. Shared credentials file (~/.aws/credentials)
	// 3. IAM role (if running on EC2/ECS/Lambda)

	// Load the AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx, configOpts...)
	if err != nil {
		return errors.Wrap(err, "failed to load AWS config")
	}

	// If region was not explicitly set, store the detected region
	if c.Region == "" {
		c.Region = cfg.Region
		// fmt.Printf("S3 Client Debug - Using detected region: %s\n", c.Region)
	}

	// Create S3 client with path style config if needed
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// Always use path style for LocalStack
		if c.PathStyle || strings.Contains(c.Endpoint, "localhost") || strings.Contains(c.Endpoint, "127.0.0.1") {
			// fmt.Printf("S3 Client Debug - Enabling path style access\n")
			o.UsePathStyle = true
		}
	})

	// Create uploader and downloader
	uploader := manager.NewUploader(s3Client)
	downloader := manager.NewDownloader(s3Client)

	// Store the client
	c.awsConfig = cfg
	c.s3Client = s3Client
	c.uploader = uploader
	c.downloader = downloader

	return nil
}

// Upload uploads a file to S3
func (c *Client) Upload(ctx context.Context, localFile string, s3Key string) error {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	// Open the file
	file, err := os.Open(localFile)
	if err != nil {
		return errors.Wrap(err, "failed to open local file")
	}
	defer file.Close()

	// Format the key
	s3Key = FormatKey(s3Key)

	// Upload the file
	_, err = c.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(s3Key),
		Body:   file,
	})

	if err != nil {
		return errors.Wrap(err, "failed to upload file to S3")
	}

	return nil
}

// Download downloads a file from S3
func (c *Client) Download(ctx context.Context, s3Key string, localFile string) error {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	// Format the key
	s3Key = FormatKey(s3Key)

	// Create the directory for the local file if it doesn't exist
	err := os.MkdirAll(filepath.Dir(localFile), 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create local directory")
	}

	// Create a file to write the S3 Object contents to
	file, err := os.Create(localFile)
	if err != nil {
		return errors.Wrap(err, "failed to create local file")
	}
	defer file.Close()

	// Download the object
	_, err = c.downloader.Download(ctx, file, &s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(s3Key),
	})

	if err != nil {
		return errors.Wrap(err, "failed to download file from S3")
	}

	return nil
}

// List lists objects in the S3 bucket with the given prefix
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return nil, fmt.Errorf("S3 client not initialized")
	}

	// Format the prefix
	prefix = FormatKey(prefix)

	// List objects with the given prefix
	var keys []string
	var continuationToken *string

	for {
		// Make ListObjectsV2 request
		resp, err := c.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.Bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})

		if err != nil {
			return nil, errors.Wrap(err, "failed to list objects in S3 bucket")
		}

		// Add keys to result
		for _, obj := range resp.Contents {
			keys = append(keys, *obj.Key)
		}

		// Check if there are more objects to fetch
		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		continuationToken = resp.NextContinuationToken
	}

	return keys, nil
}

// BucketExists checks if the specified bucket exists and is accessible
func (c *Client) BucketExists(ctx context.Context) (bool, error) {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return false, fmt.Errorf("S3 client not initialized")
	}

	// fmt.Printf("S3 Client Debug - Checking if bucket exists: %s\n", c.Bucket)

	// Localstack handling - for localstack we'll try to list the bucket instead of HeadBucket
	// since it's more reliable with localstack
	if strings.Contains(c.Endpoint, "localhost") || strings.Contains(c.Endpoint, "127.0.0.1") {
		// fmt.Printf("S3 Client Debug - Using localstack-specific bucket check\n")

		// Try to list objects with max keys 0 to just check bucket existence
		_, err := c.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  aws.String(c.Bucket),
			MaxKeys: aws.Int32(1),
		})

		if err != nil {
			// fmt.Printf("S3 Client Debug - Bucket list error: %v\n", err)
			// Check if the error is due to the bucket not existing
			if strings.Contains(err.Error(), "NoSuchBucket") ||
				strings.Contains(err.Error(), "NotFound") ||
				strings.Contains(err.Error(), "404") {
				return false, nil
			}
			return false, errors.Wrap(err, "failed to list bucket (localstack)")
		}

		// fmt.Printf("S3 Client Debug - Bucket exists (localstack check)\n")
		return true, nil
	}

	// For non-localstack, use standard HeadBucket
	// Try to access the bucket
	_, err := c.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.Bucket),
	})

	if err != nil {
		// fmt.Printf("S3 Client Debug - HeadBucket error: %v\n", err)
		// Check if the error is due to the bucket not existing
		// The NotFound struct might not be directly accessible in this version of the SDK
		// So we'll check the error message or status code
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "404") {
			return false, nil
		}
		// Check for 301 redirect which means wrong region
		if strings.Contains(err.Error(), "301") || strings.Contains(err.Error(), "MovedPermanently") {
			// For AWS S3, a 301 error means the bucket exists but is in a different region
			// We'll print a helpful warning but not fail the check
			fmt.Printf("WARNING: Failed to check if bucket exists: %v\n", err)
			fmt.Printf("This usually means the bucket '%s' exists but is in a different region than '%s'.\n", c.Bucket, c.Region)
			fmt.Printf("Please set the correct region using AWS_REGION environment variable.\n")
			// Since we can't properly verify the bucket, we'll return an error
			// This prevents potential issues with trying to use a bucket in the wrong region
			return false, fmt.Errorf("bucket appears to be in a different region (got 301 redirect)")
		}
		return false, errors.Wrap(err, "failed to check bucket existence")
	}

	// fmt.Printf("S3 Client Debug - Bucket exists (HeadBucket)\n")
	return true, nil
}

// CreateFolder creates a "folder" in S3 by creating an empty object with a key ending in /
func (c *Client) CreateFolder(ctx context.Context, folder string) error {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	// Format the folder path and ensure it ends with /
	folder = FormatKey(folder)
	if !strings.HasSuffix(folder, "/") {
		folder = folder + "/"
	}

	// Create an empty object with the folder key
	_, err := c.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(folder),
		Body:   strings.NewReader(""),
	})

	if err != nil {
		return errors.Wrap(err, "failed to create folder in S3")
	}

	return nil
}

// FolderExists checks if a folder exists in S3
func (c *Client) FolderExists(ctx context.Context, folder string) (bool, error) {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return false, fmt.Errorf("S3 client not initialized")
	}

	// Format the folder path and ensure it ends with /
	folder = FormatKey(folder)
	if !strings.HasSuffix(folder, "/") {
		folder = folder + "/"
	}

	// List objects with the folder as prefix and max count of 1
	resp, err := c.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(c.Bucket),
		Prefix:  aws.String(folder),
		MaxKeys: aws.Int32(1),
	})

	if err != nil {
		return false, errors.Wrap(err, "failed to check folder existence")
	}

	// If there are any objects with this prefix, the folder exists
	return len(resp.Contents) > 0, nil
}

// GetObject retrieves an object from S3 and returns its content as a byte slice
func (c *Client) GetObject(ctx context.Context, s3Key string) ([]byte, error) {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return nil, fmt.Errorf("S3 client not initialized")
	}

	// Format the key
	s3Key = FormatKey(s3Key)

	// Get the object
	resp, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(s3Key),
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to get object from S3")
	}
	defer resp.Body.Close()

	// Read the object content
	return io.ReadAll(resp.Body)
}

// PutObject uploads a byte slice to S3
func (c *Client) PutObject(ctx context.Context, s3Key string, data []byte) error {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	// Format the key
	s3Key = FormatKey(s3Key)

	// Upload the data
	_, err := c.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(s3Key),
		Body:   strings.NewReader(string(data)),
	})

	if err != nil {
		return errors.Wrap(err, "failed to put object to S3")
	}

	return nil
}

// ObjectExists checks if an object exists in S3
func (c *Client) ObjectExists(ctx context.Context, s3Key string) (bool, error) {
	// Ensure we have a valid client
	if c.s3Client == nil {
		return false, fmt.Errorf("S3 client not initialized")
	}

	// Format the key
	s3Key = FormatKey(s3Key)

	// Use HeadObject to check if the object exists
	_, err := c.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(s3Key),
	})

	if err != nil {
		// Check if the error is due to the object not existing
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "404") {
			return false, nil
		}
		return false, errors.Wrap(err, "failed to check object existence")
	}

	return true, nil
}

// FormatKey formats an S3 key by ensuring it doesn't have leading slashes
func FormatKey(key string) string {
	// Remove all leading slashes
	for strings.HasPrefix(key, "/") {
		key = strings.TrimPrefix(key, "/")
	}
	return key
}
