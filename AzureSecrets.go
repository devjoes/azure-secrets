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
	p.Namespace = "default"
	p.pluginHelper = ph
	p.factory = ph.ResmapFactory()
	p.loader = kv.NewLoader(p.pluginHelper.Loader(), p.pluginHelper.Validator())
	err = yaml.Unmarshal(c, p)
	return err
}

func (p *plugin) Generate() (resmap.ResMap, error) {
	resmap := resmap.New()
	kvClient, err := getKvClient(p.Vault)
	if err != nil {
		return nil, err
	}

	secretValues, err := p.getSecretValues(&kvClient)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	for _, sec := range p.Secrets {
		innerRmap, err := p.generateSecret(sec, secretValues)
		if err != nil {
			return nil, errors.Wrapf(err, "Error generating %v", sec)
		}
		resmap.AppendAll(innerRmap)
	}
	return resmap, nil
}

func getSecret(valuesChan chan secretValue, name string, kvClient *iKvClient) {
	//fmt.Printf("Getting secret '%s' in vault %v", name, *kvClient)
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
			valuesChan <- secretValue{name, "", errors.Errorf("%v", err)}
		}
	}()
	sec, err := (*kvClient).getSecret(name)
	if err != nil {
		valuesChan <- secretValue{name, "", err}
	}
	valuesChan <- secretValue{name, *sec, nil}
}

func (p *plugin) getSecretValues(kvClient *iKvClient) (map[string]string, error) {
	values := make(map[string]string)
	valuesChan := make(chan secretValue)
	secNames := p.getUniqueSecretNames()

	for _, n := range secNames {
		go getSecret(valuesChan, n, kvClient)
	}
	for range secNames {
		val := <-valuesChan
		if val.err != nil {
			close(valuesChan)
			return nil, errors.Wrapf(val.err, "Error getting secret %s", val.name)
		}
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
					if (err != nil){
						return nil, errors.Wrapf(err,"Could not base64 decode '%s'", v)
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

func getKvClient(vaultName string) (iKvClient, error) {
	// Kustomize plugins don't seem to support DI'ing mocks :(
	if vaultName == "__TESTING_AZURESECRETS__" {
		return testClient{}, nil
	}

	if os.Getenv(azureTenantID) == "" || os.Getenv(azureClientID) == "" || os.Getenv(azureClientSecret) == "" || os.Getenv(disableAzureAuthValidation) != "" {
		return nil, errors.New(fmt.Sprintf("The environment variables: %s, %s, %s should be set. Or set %s.", azureTenantID, azureTenantID, azureTenantID, disableAzureAuthValidation))
	}

	authorizer, err := kvauth.NewAuthorizerFromEnvironment()
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
	res, err := kvc.client.GetSecret(context.Background(), "https://"+kvc.vaultName+".vault.azure.net", name, "")
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