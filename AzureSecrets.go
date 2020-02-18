package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"sort"
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

type innerSecret struct {
	Name         string   `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace    string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Keys         []string `json:"keys,omitempty" yaml:"keys,omitempty"`
	Base64Decode bool     `json:"base64decode,omitempty" yaml:"base64decode,omitempty"`
}

type plugin struct {
	pluginHelper     *resmap.PluginHelpers
	types.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Vault            string        `json:"vault,omitempty" yaml:"vault,omitempty"`
	Secrets          []innerSecret `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Verbose          bool          `json:"verbose,omitempty" yaml:"verbose,omitempty"`
	async            bool          // This doesn't work
	factory          *resmap.Factory
	loader           ifc.KvLoader
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
	p.pluginHelper = ph
	p.factory = ph.ResmapFactory()
	p.loader = kv.NewLoader(p.pluginHelper.Loader(), p.pluginHelper.Validator())
	err = yaml.Unmarshal(c, p)
	p.debug("Azure Secrets - config end")
	return err
}

func (p *plugin) Generate() (resmap.ResMap, error) {
	p.debug("Azure Secrets - generate start")
	resmap := resmap.New()
	_, err := getKvClient(p.Vault)
	if err != nil {
		p.debug("Azure Secrets - generate error")
		return nil, err
	}

	var secretValues map[string]string
	if p.async {
		secretValues, err = p.getSecretValuesAsync()
	} else {
		secretValues, err = p.getSecretValues()
	}
	if err != nil {
		p.debug("Azure Secrets - generate error %v", err)
		return nil, err
	}

	for _, sec := range p.Secrets {
		innerRmap, err := p.generateSecret(sec, secretValues)
		if err != nil {
			p.debug("Azure Secrets - generate error")
			return nil, errors.Wrapf(err, "Error generating %v", sec)
		}
		resmap.AppendAll(innerRmap)
	}
	p.debug("Azure Secrets - generate end")
	return resmap, nil
}

func getSecret(valuesChan chan secretValue, name string, vaultName string) {
	fmt.Printf("Getting secret '%s' in vault %v\n", name, vaultName)
	kvClient, err := getKvClient(vaultName)
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
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

func (p *plugin) generateSecret(secret innerSecret, values map[string]string) (resmap.ResMap, error) {
	args := types.SecretArgs{}
	args.Name = secret.Name
	args.Namespace = secret.Namespace
	if args.Name == "" {
		args.Name = p.Name
	}
	if args.Namespace == "" {
		args.Namespace = p.Namespace
	}
	if args.Name == "" {
		return nil, errors.Errorf("Secret is missing name: %v", secret)
	}
	if args.Namespace == "" {
		return nil, errors.Errorf("Secret is missing namespace: %v", secret)
	}

	for _, key := range secret.Keys {
		kv := strings.Split(key, "=")
		if len(kv) == 2 {
			if v, ok := values[kv[1]]; ok {
				if secret.Base64Decode {
					data, err := base64.StdEncoding.DecodeString(v)
					if err != nil {
						return nil, errors.Wrapf(err, "Could not base64 decode '%s'", v)
					}
					v = string(data)
				}
				args.LiteralSources = append(
					args.LiteralSources, kv[0]+"="+v)
			}
		}
	}

	return p.factory.FromSecretArgs(p.loader, nil, args)
}

func (p *plugin) debug(format string, a ...interface{}) {
	if p.Verbose {
		fmt.Printf("Azure Secrets - "+format+"\n", a)
	}
}

func getKvClient(vaultName string) (iKvClient, error) {
	// Kustomize plugins don't seem to support DI'ing mocks :(
	if vaultName == "__TESTING_AZURESECRETS__" {
		return testClient{}, nil
	}

	authFile := os.Getenv(azureAuthLocation)
	if authFile == "" {
		if os.Getenv(azureTenantID) == "" || os.Getenv(azureClientID) == "" || os.Getenv(azureClientSecret) == "" || os.Getenv(disableAzureAuthValidation) != "" {
			return nil, errors.New(fmt.Sprintf("The environment variables: %s, %s, %s should be set. Or set %s.", azureTenantID, azureTenantID, azureTenantID, disableAzureAuthValidation))
		}
	} else {
		if _, err := os.Stat(authFile); os.IsNotExist(err) {
			return nil, errors.New(fmt.Sprintf("%s does not exist", authFile))
		}
	}

	var authorizer autorest.Authorizer
	var err error
	if authFile == "" {
		authorizer, err = kvauth.NewAuthorizerFromEnvironment()
		fmt.Printf("Using env based auth: %s\n", os.Getenv(azureClientID))
	} else {
		authorizer, err = kvauth.NewAuthorizerFromFile(azure.PublicCloud.ResourceManagerEndpoint)
		fmt.Printf("Using file based auth: %s\n", authFile)
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
			fmt.Println(err)
		}
	}()
	// Azure keyvault seems to randomly throw 401s at us which we have to ignore and just try again
	for !done {
		res, err = kvc.client.GetSecret(context.Background(), "https://"+kvc.vaultName+".vault.azure.net", name, "")
		done = err == nil || attempts > 5
		if err != nil {
			fmt.Printf("error %s on attempt %d\n", err.Error(), attempts)
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

// Kustomize plugins don't seem to support DI'ing mocks :(
type testClient struct {
}

func (kvc testClient) getSecret(name string) (*string, error) {
	var val string
	if name == "RND" {
		val = fmt.Sprintf("%d", rand.Int63())
	} else if strings.HasPrefix(name, "B64") {
		val = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("Secret value for %s", name[3:])))
	} else {
		val = fmt.Sprintf("Secret value for %s", name)
	}
	time.Sleep(time.Second)
	return &val, nil
}
