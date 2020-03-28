package main_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	kusttest_test "sigs.k8s.io/kustomize/api/testutils/kusttest"
	"sigs.k8s.io/yaml"
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
	testAzureSecrets_OfflineTesting(t, "", 10, 15)
}

func TestAzureSecrets_OfflineTestingCustomDelay(t *testing.T) {
	testAzureSecrets_OfflineTesting(t, "1", 2, 5)
}

func testAzureSecrets_OfflineTesting(t *testing.T, pauseForSecs string, minDuration float64, maxDuration float64) {
	offlineTestingMode := "AZURE_SECRETS_OFFLINE_TESTING_MODE"
	warnForSeconds := "AZURE_SECRETS_OFFLINE_TESTING_MODE_WARN_SECONDS"
	os.Setenv(offlineTestingMode, "1")
	os.Setenv(warnForSeconds, pauseForSecs)
	start := time.Now()
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")
	result1 := th.LoadAndRunGenerator(simpleTestInput)
	result2 := th.LoadAndRunGenerator(simpleTestInput)
	os.Setenv(offlineTestingMode, "")
	os.Setenv(warnForSeconds, "")

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

	durationSecs := time.Since(start).Seconds()
	if durationSecs < minDuration && durationSecs > maxDuration {
		desiredDuration, e := strconv.Atoi(pauseForSecs)
		if e != nil {
			desiredDuration = 5
		}
		t.Errorf("When %s is set the user should be told and the plugin should pause for %d secs not %d", offlineTestingMode, desiredDuration*2, int(durationSecs))
	}

	os.Setenv(offlineTestingMode, "1")
	result3 := th.LoadAndRunGenerator(strings.ReplaceAll(simpleTestInput, "base64decode: false", "base64decode: true"))
	os.Setenv(offlineTestingMode, "")
	_, err = result3.AsYaml()
	if err != nil {
		t.Error(err)
	}

}

func TestAzureSecrets_OutputAsConfigMap(t *testing.T) {
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
  outputAsConfigMap: true
  keys:
  - FOOKey=RND
  - BARKey=RND`)

	yamlResult, _ := result.AsYaml()

	if !regexp.MustCompile(`ConfigMap`).Match(yamlResult) {
		t.Errorf("Could not find Kind: ConfigMap in:\n%s", string(yamlResult))
	}

	if regexp.MustCompile(`Secret`).Match(yamlResult) {
		t.Errorf("Found Kind: Secret in:\n%s", string(yamlResult))
	}
}

func testAzureSecrets_OnError(t *testing.T, exclude bool) []byte {
	th := kusttest_test.MakeEnhancedHarness(t).
		BuildGoPlugin("devjoes", "v1", "AzureSecrets")
	result := th.LoadAndRunGenerator(fmt.Sprintf(`apiVersion: devjoes/v1
kind: AzureSecrets
metadata:
  name: default-name
  namespace: default-ns
vault: __TESTING_AZURESECRETS__
onError:
  warn: true
  exclude: %v
  patchMetadata:
    annotations:
      foo: bar
secrets:
- name: test-secret
  namespace: test-ns
  base64decode: false
  keys:
  - ERR=ERR`, exclude))
	yamlResult, err := result.AsYaml()
	if err != nil {
		t.Error(err)
	}
	return yamlResult
}

func TestAzureSecrets_OnErrorWarnAndPatch(t *testing.T) {
	yamlResult := testAzureSecrets_OnError(t, false)
	secret := v1.Secret{}
	err := yaml.Unmarshal(yamlResult, &secret)
	if err != nil {
		t.Errorf("Failed to unmarshal %s %v", string(yamlResult), err)
	}
	if secret.GetObjectMeta().GetAnnotations()["foo"] != "bar" {
		t.Errorf("Annotation missing %s", yamlResult)
	}
}

func TestAzureSecrets_OnErrorWarnAndExclude(t *testing.T) {
	yamlResult := testAzureSecrets_OnError(t, true)
	if len(yamlResult) != 0 {
		t.Errorf("Expected empty result, got %s", string(yamlResult))
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
