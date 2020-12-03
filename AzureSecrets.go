package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
	kvauth "github.com/Azure/azure-sdk-for-go/services/keyvault/auth"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"

	"github.com/pkg/errors"
	"sigs.k8s.io/kustomize/api/ifc"
	"sigs.k8s.io/kustomize/api/kv"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"
)

const azureTenantID = "AZURE_TENANT_ID"
const azureClientID = "AZURE_CLIENT_ID"
const azureClientSecret = "AZURE_CLIENT_SECRET"
const azureAuthLocation = "AZURE_AUTH_LOCATION"
const disableAzureAuthValidation = "DISABLE_AZURE_AUTH_VALIDATION"
const offlineTestingMode = "AZURE_SECRETS_OFFLINE_TESTING_MODE"
const warnForSeconds = "AZURE_SECRETS_OFFLINE_TESTING_MODE_WARN_SECONDS"

type innerSecret struct {
	Name              string   `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace         string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Keys              []string `json:"keys,omitempty" yaml:"keys,omitempty"`
	Base64Decode      bool     `json:"base64decode,omitempty" yaml:"base64decode,omitempty"`
	OutputAsConfigMap bool     `json:"outputAsConfigMap,omitempty" yaml:"outputAsConfigMap,omitempty"`
	Errored           bool
	Options           *types.GeneratorOptions
}

type plugin struct {
	pluginHelper     *resmap.PluginHelpers
	types.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Vault            string        `json:"vault,omitempty" yaml:"vault,omitempty"`
	Secrets          []innerSecret `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Verbose          bool          `json:"verbose,omitempty" yaml:"verbose,omitempty"`
	OnError          errorOptions  `json:"onError,omitempty" yaml:"onError,omitempty"`
	async            bool          // This doesn't work
	factory          *resmap.Factory
	loader           ifc.KvLoader
}

type errorOptions struct {
	Warn          bool                   `json:"warn,omitempty" yaml:"warn,omitempty"`
	Exclude       bool                   `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	PatchMetadata types.GeneratorOptions `json:"patchMetadata,omitempty" yaml:"patchMetadata,omitempty"`
}

type secretValue struct {
	name  string
	value string
	err   error
}

var KustomizePlugin plugin

func (p *plugin) Config(ph *resmap.PluginHelpers, c []byte) (err error) {
	p.debug("Azure Secrets - config start")
	p.Namespace = "default"
	p.OnError = errorOptions{
		Warn:          false,
		PatchMetadata: types.GeneratorOptions{},
	}
	p.pluginHelper = ph
	p.factory = ph.ResmapFactory()
	p.loader = kv.NewLoader(p.pluginHelper.Loader(), p.pluginHelper.Validator())
	err = yaml.Unmarshal(c, p)
	p.debug("Azure Secrets - config end")
	return err
}

func (p *plugin) Generate() (resmap.ResMap, error) {
	p.debug("Azure Secrets - generate start")
	var outerResmap resmap.ResMap
	_, err := getKvClient(p.Vault)
	if err != nil {
		p.debug("Azure Secrets - generate error")
		return nil, err
	}
	var options *types.GeneratorOptions
	options = nil
	var secretValues map[string]string
	if p.async {
		secretValues, err = p.getSecretValuesAsync()
	} else {
		secretValues, err = p.getSecretValues()
	}
	if err != nil {
		p.debug("Azure Secrets - generate error %v", err)

		if err != nil {
			p.debug("Azure Secrets - generate error")
			outerResmap, secretValues, options, err = p.handleError(err)
			if err != nil {
				return nil, err
			}
			if outerResmap != nil {
				return outerResmap, nil
			}
		}
	}

	outerResmap = resmap.New()
	for _, sec := range p.Secrets {
		var innerResmap resmap.ResMap
		var err error
		if sec.OutputAsConfigMap {
			innerResmap, err = p.outputAsConfigMap(sec, secretValues, options)
		} else {
			innerResmap, err = p.generateSecret(sec, secretValues, options)
		}
		if err != nil {
			p.debug("Azure Secrets - generate error")
			return nil, err
		}
		outerResmap.AppendAll(innerResmap)
	}
	p.debug("Azure Secrets - generate end")
	return outerResmap, nil
}

func (p *plugin) handleError(err error) (resmap.ResMap, map[string]string, *types.GeneratorOptions, error) {
	yml, _ := yaml.Marshal(p)
	if !p.OnError.Warn {
		return nil, nil, nil, errors.Wrapf(err, "Error generating %s %s", yml, p.Name)
	}
	fmt.Fprintf(os.Stderr, "AZURESECERTS WARNING: Error '%s' generating secret %s\n", err.Error(), p.Name)
	if p.OnError.Exclude {
		return resmap.New(), nil, nil, nil
	}
	secNames := p.getUniqueSecretNames()
	secValues := make(map[string]string, len(secNames))
	for i := 0; i < len(secNames); i++ {
		// We add some random character to the end of the value in case it being use to define something like a password
		// We don't want someone to be able to force a system in to a state where an important password becomes "ERROR"
		secValues[secNames[i]] = base64.StdEncoding.EncodeToString([]byte("ERROR_" + string(getRandomChars(32))))
	}
	return nil, secValues, &p.OnError.PatchMetadata, nil
}

func getSecret(valuesChan chan secretValue, name string, vaultName string) {
	fmt.Fprintf(os.Stderr, "Getting secret '%s' in vault %v\n", name, vaultName)
	kvClient, err := getKvClient(vaultName)
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintf(os.Stderr, "%v", err)
			valuesChan <- secretValue{name, "", errors.Errorf("%v", err)}
		}
	}()
	sec, err := kvClient.getSecret(name)
	if err != nil {
		valuesChan <- secretValue{name, "", err}
	}
	valuesChan <- secretValue{name, *sec, nil}
}

func (p *plugin) getSecretValues() (map[string]string, error) {
	kvClient, err := getKvClient(p.Vault)
	if err != nil {
		p.debug("Error getting client %v", err)
		return nil, errors.Wrapf(err, "Error getting client")
	}
	secNames := p.getUniqueSecretNames()
	values := make(map[string]string)

	for _, n := range secNames {
		p.debug("Getting value for %s", n)
		sec, err := kvClient.getSecret(n)
		if err != nil {
			p.debug("Error getting secret %s %v", n, err)
			return nil, err
		}
		values[n] = *sec
	}
	return values, nil
}

func (p *plugin) getSecretValuesAsync() (map[string]string, error) {
	p.debug("Get Secret Values Start")
	values := make(map[string]string)
	valuesChan := make(chan secretValue)
	secNames := p.getUniqueSecretNames()

	for _, n := range secNames {
		p.debug("Getting value for %s", n)
		go getSecret(valuesChan, n, p.Vault)
	}
	for range secNames {
		val := <-valuesChan
		if val.err != nil {
			p.debug("Error from channel %v", val.err)
			close(valuesChan)
			return nil, errors.Wrapf(val.err, "Error getting secret %s", val.name)
		}
		p.debug("Got %s for %s", val.name, val.value)
		values[val.name] = val.value
	}
	return values, nil
}

func (p *plugin) getUniqueSecretNames() []string {
	var keys []string
	for _, s := range p.Secrets {
		for _, key := range s.Keys {
			kv := strings.Split(key, "=")
			if !contains(keys, kv[1]) {
				keys = append(keys, kv[1])
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func contains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
			return true
		}
	}
	return false
}

func (p *plugin) generateSecret(secret innerSecret, values map[string]string, options *types.GeneratorOptions) (resmap.ResMap, error) {
	name, namespace, contents, err := p.generateContents(secret, values)
	if err != nil {
		return nil, err
	}
	args := types.SecretArgs{}
	args.Name = name
	args.Namespace = namespace
	args.LiteralSources = contents
	return p.factory.FromSecretArgs(p.loader, options, args)
}

func (p *plugin) outputAsConfigMap(secret innerSecret, values map[string]string, options *types.GeneratorOptions) (resmap.ResMap, error) {
	name, namespace, contents, err := p.generateContents(secret, values)
	if err != nil {
		return nil, err
	}
	args := types.ConfigMapArgs{}
	args.Name = name
	args.Namespace = namespace
	args.LiteralSources = contents
	return p.factory.FromConfigMapArgs(p.loader, options, args)
}

func (p *plugin) generateContents(secret innerSecret, values map[string]string) (string, string, []string, error) {
	name := secret.Name
	namespace := secret.Namespace
	var contents []string

	if name == "" {
		name = p.Name
	}
	if namespace == "" {
		namespace = p.Namespace
	}
	if name == "" {
		return "", "", nil, errors.Errorf("Secret is missing name: %v", secret)
	}
	if namespace == "" {
		return "", "", nil, errors.Errorf("Secret is missing namespace: %v", secret)
	}

	for _, key := range secret.Keys {
		kv := strings.Split(key, "=")
		if len(kv) == 2 {
			if v, ok := values[kv[1]]; ok {
				if secret.Base64Decode {
					data, err := base64.StdEncoding.DecodeString(v)
					if err != nil {
						return "", "", nil, errors.Wrapf(err, "Could not base64 decode '%s'", v)
					}
					v = string(data)
				}
				contents = append(contents, kv[0]+"="+v)
			}
		}
	}
	return name, namespace, contents, nil
}

func (p *plugin) debug(format string, a ...interface{}) {
	if p.Verbose {
		fmt.Fprintf(os.Stderr, "Azure Secrets - "+format+"\n", a)
	}
}

func getKvClient(vaultName string) (iKvClient, error) {
	if os.Getenv(offlineTestingMode) != "" {
		return randomSecretClient{warnedUser: false, vaultName: vaultName}, nil
	}
	// Kustomize plugins don't seem to support DI'ing mocks :(
	if vaultName == "__TESTING_AZURESECRETS__" {
		return testClient{}, nil
	}

	authFile := os.Getenv(azureAuthLocation)
	if os.Getenv(disableAzureAuthValidation) == "" {
		if authFile == "" {
			if os.Getenv(azureTenantID) == "" || os.Getenv(azureClientID) == "" || os.Getenv(azureClientSecret) == "" {
				return nil, errors.New(fmt.Sprintf("The environment variables: %s, %s, %s should be set. Or set %s to bypass this check.", azureTenantID, azureClientID, azureClientSecret, disableAzureAuthValidation))
			}
		} else {
			if _, err := os.Stat(authFile); os.IsNotExist(err) {
				return nil, errors.New(fmt.Sprintf("%s does not exist", authFile))
			}
		}
	}

	var authorizer autorest.Authorizer
	var err error
	if authFile == "" {
		authorizer, err = kvauth.NewAuthorizerFromEnvironment()
		fmt.Fprintf(os.Stderr, "Using env based auth: %s\n", os.Getenv(azureClientID))
	} else {
		authorizer, err = kvauth.NewAuthorizerFromFile(azure.PublicCloud.ResourceManagerEndpoint)
		fmt.Fprintf(os.Stderr, "Using file based auth: %s\n", authFile)
	}
	if err != nil {
		return nil, errors.Wrap(err, "unable to create vault authorizer")
	}

	basicClient := keyvault.New()
	basicClient.Authorizer = authorizer
	client := azKvClient{&basicClient, vaultName}

	return client, nil
}

type iKvClient interface {
	getSecret(name string) (*string, error)
}

type azKvClient struct {
	client    *keyvault.BaseClient
	vaultName string
}

func (kvc azKvClient) getSecret(name string) (*string, error) {
	done := false
	attempts := 0
	var err error
	var res keyvault.SecretBundle
	defer func() {
		if recoveredErr := recover(); err != nil {
			err = errors.Errorf("Error getting secret '%s' from vault '%s' %v", name, kvc.vaultName, recoveredErr)
			fmt.Fprintf(os.Stderr, "%v", err)
		}
	}()
	// Azure keyvault seems to randomly throw 401s at us which we have to ignore and just try again
	for !done {
		res, err = kvc.client.GetSecret(context.Background(), "https://"+kvc.vaultName+".vault.azure.net", name, "")
		done = err == nil || attempts > 5
		if err != nil {
			fmt.Fprintf(os.Stderr, "error %s on attempt %d\n", err.Error(), attempts)
			if !strings.Contains(err.Error(), "401") {
				// done = true
			}
		}
		attempts++
	}
	if err != nil {
		return nil, errors.Wrapf(err, "Error getting secret '%s' from vault '%s'", name, kvc.vaultName)
	}
	return res.Value, nil
}

var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

func getRandomChars(count int) []byte {
	const charset = "1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var rndBytes = make([]byte, count)
	for i := range rndBytes {
		rndBytes[i] = charset[rnd.Intn(len(charset))]
	}
	return rndBytes
}

type randomSecretClient struct {
	warnedUser bool
	vaultName  string
}

func (kvc randomSecretClient) warnUser() {
	if kvc.warnedUser {
		return
	}
	const red = "\033[1;31m"
	const noColour = "\033[0m"
	fmt.Fprintf(os.Stderr, "\n%sWarning whilst reading from vault:\n%s%s\n", red, kvc.vaultName, noColour)
	fmt.Fprintf(os.Stderr, "%s#########################################%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#                                       #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#             AZURE SECRETS             #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#       IS IN OFFLINE TESTING MODE      #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#        RETURNING RANDOM STRINGS       #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#        INSTEAD OF REAL SECRETS!       #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#                                       #%s\n", red, noColour)
	fmt.Fprintf(os.Stderr, "%s#########################################%s\n", red, noColour)

	secs, err := strconv.Atoi(os.Getenv(warnForSeconds))
	if err != nil {
		secs = 5
	}
	time.Sleep(time.Second * time.Duration(secs))
	kvc.warnedUser = true
}

func (kvc randomSecretClient) getSecret(_ string) (*string, error) {
	kvc.warnUser()
	secret := base64.StdEncoding.EncodeToString(getRandomChars(32))
	return &secret, nil
}

// Kustomize plugins don't seem to support DI'ing mocks :(
type testClient struct {
}

func (kvc testClient) getSecret(name string) (*string, error) {
	var val string
	if name == "ERR" {
		return nil, errors.Errorf("test error")
	} else if name == "RND" {
		val = fmt.Sprintf("%d", rand.Int63())
	} else if strings.HasPrefix(name, "B64") {
		val = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("Secret value for %s", name[3:])))
	} else {
		val = fmt.Sprintf("Secret value for %s", name)
	}
	time.Sleep(time.Second)
	return &val, nil
}
