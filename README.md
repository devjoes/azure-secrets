# Azure Secrets

This Kustomize plugin reads secrets from an Azure Key Vault and outputs them as YAML.

## Usage

You can declare the plugin like this:

    apiVersion: devjoes/v1
    kind: AzureSecrets
    metadata:
      name: azuresecrets
    vault: **name of the azure keyvault**
    secrets:
    - name: secret1
      namespace: test-ns
      keys:
      - foo=name_of_foo_secret_in_vault
      - bar=name_of_bar_secret_in_vault
    - name: secret2
      namespace: test-ns
      base64decode: true
      keys:
      - baz=name_of_baz_secret_in_vault
    - name: configmap
      namespace: test-ns
      outputAsConfigMap: true
      keys:
      - foo=name_of_foo_secret_in_vault
      - bar=name_of_bar_secret_in_vault

This will result in two secrets and one configmap being generated in the test-ns namespace. 
* secret1 will contain the keys foo and bar, assigned to these will be the values of the keyvault secrets 'name_of_foo_secret_in_vault' and 'name_of_bar_secret_in_vault'.
* secret2 will contain the key baz which will have the base64 decoded value of the keyvault secret 'name_of_baz_secret_in_vault'
* configmap will be identical to secret1, except as a ConfigMap

If the name or namespace of a secret is unset then it will default to the name/namespace of the parent AzureSecrets. See the Dockerfile for more examples.

If a secret cannot be read then the plugin will fail. There are certain scenarios where you do not want an entire deployment to fail. For instance when you are using a GitOps model and are building the YAML for an entire multitenanted cluster. You do not what the entire deployment process to fail because one team deleted a secret from a key vault. The onError lets you handle this.

    apiVersion: devjoes/v1
    kind: AzureSecrets
      onError:
         warn: true
         exclude: false
         patchMetadata:
           annotations:
             fluxcd.io/ignore: "true"
           labels:
             secretStatus: invalid

* If warn is true then it will print a warning to STDERR, if it is false then the plugin will fail.
* If exclude is true then no secret will be output
* If present patchMetadata allows you to set the [GeneratorOptions](https://github.com/kubernetes-sigs/kustomize/blob/master/examples/generatorOptions.md) which can be used to change the metadata of the secret. 

If exclude it not set then a secret will still be output. The secret's keys will be set to "ERROR" and then some random characters. This is to prevent an attacker from causing an issue and forcing a password to become "ERROR".


## Installation

This has been tested with Kustomize 3.5.4 (see docker file)

    go get -d github.com/devjoes/azure-secrets/
    mkdir -p ~/.config/kustomize/plugin/devjoes/v1/azuresecrets/
    go build -buildmode plugin -o ~/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so ./AzureSecrets.go

There is a Docker image [here](https://hub.docker.com/r/joeshearn/azure-secrets). You can either run this as it is, use it as a base image or copy the relevant files out of it like this:

    FROM alpine:latest
    COPY --from=joeshearn/azure-secrets /bin/kustomize /bin/kustomize
    COPY --from=joeshearn/azure-secrets /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so


## Authentication

You have to handle how the plugin authenticates with Azure, you can do this by setting the following environment variables:

* AZURE_TENANT_ID - The tenant ID where your keyvault resides.
* AZURE_CLIENT_ID - The client ID of a service principal with Read access to the vault.
* AZURE_CLIENT_SECRET - The client secret of the service principal with Read access to the vault.
* AZURE_AUTH_LOCATION - This is an alternative to the above settings - see [here for more info](https://docs.microsoft.com/en-us/azure/go/azure-sdk-go-authorization).
* DISABLE_AZURE_AUTH_VALIDATION - This bypasses all of the above options and assumes that you are handeling authentication yourself.

### Caveats

There seem to be (https://github.com/Azure/go-autorest/issues/290)[issues with go-autorest] these would be fixed by upgrading to a later version of go-autorest, but as a plugin we are constrained by Kustomize's dependencies.
* Ideally you should be able to authenticate using a service principal's client ID and secret. However I have seen issus with this when running from CI.
* Running from CI I have had a bit more success using AZURE_AUTH_LOCATION (it eventually worked after multiple retries)
* In the end I ended up using an [alpine image with the Azure CLI](https://hub.docker.com/r/joeshearn/az-cli), running az login to log in with the service principal and then bypassing authentication using DISABLE_AZURE_AUTH_VALIDATION

### Local testing

When running locally you can set the environmnet variable AZURE_SECRETS_OFFLINE_TESTING_MODE. This will make the plugin return random strings as secrets. This allows you to test your kustomize configuration without distributing the secrets required to access actual secrets.