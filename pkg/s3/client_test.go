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
	"os"
	"testing"
)

func TestNewClientFromURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "Valid S3 URL",
			url:     "s3://my-bucket/path/to/object?region=us-west-2",
			wantErr: false,
		},
		{
			name:    "Invalid URL",
			url:     "not a url",
			wantErr: true,
		},
		{
			name:    "HTTP URL",
			url:     "http://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClientFromURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClientFromURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFormatKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "No leading slash",
			key:  "path/to/object",
			want: "path/to/object",
		},
		{
			name: "With leading slash",
			key:  "/path/to/object",
			want: "path/to/object",
		},
		{
			name: "Multiple leading slashes",
			key:  "///path/to/object",
			want: "path/to/object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatKey(tt.key); got != tt.want {
				t.Errorf("FormatKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewClientFromEnv(t *testing.T) {
	// Save original environment variables to restore later
	origAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	origSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	origEndpoint := os.Getenv("AWS_ENDPOINT")
	origRegion := os.Getenv("AWS_REGION")
	origPathStyle := os.Getenv("AWS_S3_FORCE_PATH_STYLE")

	// Restore environment variables after the test
	defer func() {
		os.Setenv("AWS_ACCESS_KEY_ID", origAccessKey)
		os.Setenv("AWS_SECRET_ACCESS_KEY", origSecretKey)
		os.Setenv("AWS_ENDPOINT", origEndpoint)
		os.Setenv("AWS_REGION", origRegion)
		os.Setenv("AWS_S3_FORCE_PATH_STYLE", origPathStyle)
	}()

	tests := []struct {
		name        string
		bucket      string
		envVars     map[string]string
		wantErr     bool
		checkClient func(*Client) bool
	}{
		{
			name:   "Basic configuration",
			bucket: "test-bucket",
			envVars: map[string]string{
				"AWS_ACCESS_KEY_ID":     "test-access-key",
				"AWS_SECRET_ACCESS_KEY": "test-secret-key",
				"AWS_REGION":            "us-west-2",
			},
			wantErr: false,
			checkClient: func(c *Client) bool {
				return c.Bucket == "test-bucket" &&
					c.Region == "us-west-2" &&
					c.AccessKey == "test-access-key" &&
					c.SecretKey == "test-secret-key"
			},
		},
		{
			name:   "With endpoint",
			bucket: "test-bucket",
			envVars: map[string]string{
				"AWS_ACCESS_KEY_ID":     "test-access-key",
				"AWS_SECRET_ACCESS_KEY": "test-secret-key",
				"AWS_REGION":            "us-west-2",
				"AWS_ENDPOINT":          "https://s3.example.com",
			},
			wantErr: false,
			checkClient: func(c *Client) bool {
				return c.Bucket == "test-bucket" &&
					c.Region == "us-west-2" &&
					c.Endpoint == "https://s3.example.com"
			},
		},
		{
			name:   "With path style",
			bucket: "test-bucket",
			envVars: map[string]string{
				"AWS_REGION":              "us-west-2",
				"AWS_S3_FORCE_PATH_STYLE": "true",
			},
			wantErr: false,
			checkClient: func(c *Client) bool {
				return c.Bucket == "test-bucket" &&
					c.PathStyle == true
			},
		},
		{
			name:   "With localhost endpoint (should force path style)",
			bucket: "test-bucket",
			envVars: map[string]string{
				"AWS_REGION":   "us-west-2",
				"AWS_ENDPOINT": "http://localhost:4566",
			},
			wantErr: false,
			checkClient: func(c *Client) bool {
				return c.Bucket == "test-bucket" &&
					c.PathStyle == true &&
					c.Endpoint == "http://localhost:4566" &&
					c.SSLMode == false
			},
		},
		{
			name:    "Default region when not specified",
			bucket:  "test-bucket",
			envVars: map[string]string{},
			wantErr: false,
			checkClient: func(c *Client) bool {
				return c.Bucket == "test-bucket" &&
					c.Region == "us-east-1" // Default region
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables for this test case
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			// Clear any environment variables not set in this test case
			for _, envVar := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "AWS_ENDPOINT", "AWS_S3_FORCE_PATH_STYLE"} {
				if _, exists := tt.envVars[envVar]; !exists {
					os.Unsetenv(envVar)
				}
			}

			client, err := NewClientFromEnv(tt.bucket)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClientFromEnv() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && !tt.checkClient(client) {
				t.Errorf("NewClientFromEnv() client properties don't match expected values")
			}
		})
	}
}
