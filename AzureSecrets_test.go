package main_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	kusttest_test "sigs.k8s.io/kustomize/api/testutils/kusttest"
)

var fooSecret string = base64.StdEncoding.EncodeToString([]byte("Secret value for FOO"))
var barSecret string = base64.StdEncoding.EncodeToString([]byte("Secret value for BAR"))
var simpleTestInput string = `apiVersion: devjoes/v1
kind: AzureSecrets
metadata:
  name: default-name
  namespace: default-ns
vault: __TESTING_AZURESECRETS__
secrets:
- name: test-secret
  namespace: test-ns
  base64decode: false
  keys:
  - FOOKey=FOO
  - BARKey=BAR
- name: test-secret
  keys:
  - FOOKey=FOO
  - BARKey=BAR
- namespace: test-ns
  keys:
  - FOOKey=FOO
  - BARKey=BAR`

func TestAzureSecrets(t *testing.T) {
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")

	result := th.LoadAndRunGenerator(simpleTestInput)
	th.AssertActualEqualsExpected(result, `apiVersion: v1
data:
  BARKey: `+barSecret+`
  FOOKey: `+fooSecret+`
kind: Secret
metadata:
  name: test-secret
  namespace: test-ns
type: Opaque
---
apiVersion: v1
data:
  BARKey: `+barSecret+`
  FOOKey: `+fooSecret+`
kind: Secret
metadata:
  name: test-secret
  namespace: default-ns
type: Opaque
---
apiVersion: v1
data:
  BARKey: `+barSecret+`
  FOOKey: `+fooSecret+`
kind: Secret
metadata:
  name: default-name
  namespace: test-ns
type: Opaque
`)
}

func TestAzureSecrets_Base64Decodes(t *testing.T) {
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")

	result := th.LoadAndRunGenerator(
		`apiVersion: devjoes/v1
kind: AzureSecrets
metadata:
  name: default-name
  namespace: default-ns
vault: __TESTING_AZURESECRETS__
secrets:
- name: test-secret
  namespace: test-ns
  base64decode: true
  keys:
  - FOOKey=B64FOO
  - BARKey=B64BAR`)
	th.AssertActualEqualsExpected(result, `apiVersion: v1
data:
  BARKey: `+barSecret+`
  FOOKey: `+fooSecret+`
kind: Secret
metadata:
  name: test-secret
  namespace: test-ns
type: Opaque
`)
}

func TestAzureSecrets_QueryVaultOncePerSecret(t *testing.T) {
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")
	result := th.LoadAndRunGenerator(
		`apiVersion: devjoes/v1
kind: AzureSecrets
metadata:
  name: default-name
  namespace: default-ns
vault: __TESTING_AZURESECRETS__
secrets:
- name: test-secret1
  namespace: test-ns
  keys:
  - FOOKey=RND
  - BARKey=RND
- name: test-secret2
  namespace: test-ns
  keys:
  - FOOKey=RND
  - BARKey=RND`)

	yamlResult, _ := result.AsYaml()
	rx := regexp.MustCompile(`Key: .*`)
	secrets := rx.FindAllString(string(yamlResult), -1)
	sort.Strings(secrets)
	if len(secrets) == 1 || secrets[0] != secrets[len(secrets)-1] {
		t.Errorf("Found different values for cached secret RND %v", secrets)
	}
}

func TestAzureSecrets_OfflineTesting(t *testing.T) {
	offlineTestingMode := "AZURE_SECRETS_OFFLINE_TESTING_MODE"
	os.Setenv(offlineTestingMode, "1")
	start := time.Now()
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")
	result1 := th.LoadAndRunGenerator(simpleTestInput)
	result2 := th.LoadAndRunGenerator(simpleTestInput)
	os.Setenv(offlineTestingMode, "")

	bResult1, err := result1.AsYaml()
	if err != nil {
		t.Error(err)
	}
	bResult2, err := result2.AsYaml()
	if err != nil {
		t.Error(err)
	}
	if bytes.Compare(bResult1, bResult2) == 0 {
		t.Errorf("Returned the same result twice. When %s is set random results should be returned", offlineTestingMode)
	}
	if time.Since(start).Seconds() < 10 {
		t.Errorf("When %s is set the user should be told and the plugin should pause", offlineTestingMode)
	}

	os.Setenv(offlineTestingMode, "1")
	result3 := th.LoadAndRunGenerator(strings.ReplaceAll(simpleTestInput, "base64decode: false", "base64decode: true"))
	os.Setenv(offlineTestingMode, "")
	_, err = result3.AsYaml()
	if err != nil {
		t.Error(err)
	}

}

// func TestAzureSecrets_RunInParallel(t *testing.T) {
// 	// The test implementation takes ~1000ms to get a secret
// 	th := kusttest_test.MakeEnhancedHarness(t).
// 		BuildGoPlugin("devjoes", "v1", "AzureSecrets")
// 	start := time.Now()
// 	th.LoadAndRunGenerator(
//     `apiVersion: devjoes/v1
// kind: AzureSecrets
// metadata:
//   name: default-name
//   namespace: default-ns
// vault: __TESTING_AZURESECRETS__
// secrets:
// - name: test-secret1
//   namespace: test-ns
//   keys:
//   - FOOKey=foo`)
// 	singleDuration := time.Since(start)
// 	start = time.Now()
// 	th.LoadAndRunGenerator(
//     `apiVersion: devjoes/v1
// kind: AzureSecrets
// metadata:
//   name: default-name
// vault: __TESTING_AZURESECRETS__
// secrets:
// - name: test-secret1
//   namespace: test-ns
//   keys:
//   - FOOKey=foo1
//   - BARKey=bar1
//   - BAZKey=baz1
// - name: test-secret2
//   namespace: test-ns
//   keys:
//   - FOOKey=foo2
//   - BARKey=bar2
//   - BAZKey=baz2
// - name: test-secret3
//   namespace: test-ns
//   keys:
//   - FOOKey=foo3
//   - BARKey=bar3
//   - BAZKey=baz3`)
// 	nineDuration := time.Since(start)

// 	if nineDuration > singleDuration*2 {
// 		t.Errorf("Retrieving 9 secrets took %f times linger than retrieving a single secret - expected <2 times", float64(nineDuration.Milliseconds())/float64(singleDuration.Milliseconds()))
// 	}
// }
