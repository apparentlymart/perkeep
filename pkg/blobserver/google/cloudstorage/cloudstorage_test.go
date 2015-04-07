/*
Copyright 2014 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cloudstorage

import (
	"flag"
	"log"
	"path"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/constants/google"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/googlestorage"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/oauthutil"

	"camlistore.org/third_party/golang.org/x/oauth2"
)

var (
	// TODO(mpl): use a config file generated with the help of googinit, like for googlestorage tests.
	// And remove the 'camlistore-*-test' naming requirement ?
	bucket       = flag.String("bucket", "", "Bucket name to use for testing. If empty, testing is skipped. If non-empty, it must begin with 'camlistore-' and end in '-test' and have zero items in it.")
	clientID     = flag.String("client_id", "", "OAuth2 client_id for testing")
	clientSecret = flag.String("client_secret", "", "OAuth2 client secret for testing")
	tokenCache   = flag.String("token_cache", ".tokencache", "Token cache file.")
	authCode     = flag.String("auth_code", "", "Use when instructed to do so, when the --token_cache is empty.")
)

func TestStorage(t *testing.T) {
	testStorage(t, "")
}

func TestStorageWithBucketDir(t *testing.T) {
	testStorage(t, "/bl/obs/")
}

func testStorage(t *testing.T, bucketDir string) {
	if *bucket == "" {
		t.Skip("Skipping test without --bucket flag")
	}
	if !strings.HasPrefix(*bucket, "camlistore-") || !strings.HasSuffix(*bucket, "-test") {
		t.Fatalf("bogus bucket name %q; must begin with 'camlistore-' and end in '-test'", *bucket)
	}
	if *clientID == "" || *clientSecret == "" {
		t.Fatal("--client_id and --client_secret required. Obtain from https://console.developers.google.com/ > Project > APIs & Auth > Credentials. Should be a 'native' or 'Installed application'")
	}

	config := &oauth2.Config{
		Scopes:       []string{googlestorage.Scope},
		Endpoint:     google.Endpoint,
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
		RedirectURL:  oauthutil.TitleBarRedirectURL,
	}
	token, err := oauth2.ReuseTokenSource(nil,
		&oauthutil.TokenSource{
			Config:    config,
			CacheFile: *tokenCache,
			AuthCode: func() string {
				if *authCode == "" {
					t.Skipf("Re-run using --auth_code= with the value obtained from %s",
						config.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.ApprovalForce))
					return ""
				}
				return *authCode
			},
		}).Token()
	if err != nil {
		t.Fatalf("could not acquire token: %v", err)
	}

	bucketWithDir := path.Join(*bucket, bucketDir)

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"bucket": bucketWithDir,
				"auth": map[string]interface{}{
					"client_id":     *clientID,
					"client_secret": *clientSecret,
					"refresh_token": token.RefreshToken,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !testing.Short() {
				log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
			}
			if bucketWithDir != *bucket {
				// Adding "a", and "c" objects in the bucket to make sure objects out of the
				// "directory" are not touched and have no influence.
				for _, key := range []string{"a", "c"} {
					err := sto.(*Storage).client.PutObject(
						&googlestorage.Object{Bucket: sto.(*Storage).bucket, Key: key},
						strings.NewReader(key))
					if err != nil {
						t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*Storage).bucket, err)
					}
				}
			}

			clearBucket := func(beforeTests bool) func() {
				return func() {
					var all []blob.Ref
					blobserver.EnumerateAll(context.New(), sto, func(sb blob.SizedRef) error {
						t.Logf("Deleting: %v", sb.Ref)
						all = append(all, sb.Ref)
						return nil
					})
					if err := sto.RemoveBlobs(all); err != nil {
						t.Fatalf("Error removing blobs during cleanup: %v", err)
					}
					if beforeTests {
						return
					}
					if bucketWithDir != *bucket {
						// checking that "a" and "c" at the root were left untouched.
						for _, key := range []string{"a", "c"} {
							if _, _, err := sto.(*Storage).client.GetObject(&googlestorage.Object{Bucket: sto.(*Storage).bucket,
								Key: key}); err != nil {
								t.Fatalf("could not find object %s after tests: %v", key, err)
							}
							if err := sto.(*Storage).client.DeleteObject(&googlestorage.Object{Bucket: sto.(*Storage).bucket, Key: key}); err != nil {
								t.Fatalf("could not remove object %s after tests: %v", key, err)
							}

						}
					}
				}
			}
			clearBucket(true)()
			return sto, clearBucket(false)
		},
	})
}
