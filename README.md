# Azure Secrets

This Kustomize plugin reads secrets from an Azure Key Vault and outputs them as YAML.

## Authentication

You have to handle how the plugin authenticates with Azure, you can do this by setting the following environment variables:

* AZURE_TENANT_ID - The tenant ID where your keyvault resides.
* AZURE_CLIENT_ID - The client ID of a service principal with Read access to the vault.
* AZURE_CLIENT_SECRET - The client secret of the service principal with Read access to the vault.

Or if you do not want to use a service principle then you can set DISABLE_AZURE_AUTH_VALIDATION to a non-empty value. This will assume that you have authenticated some other way (like MSI).

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
      keys:
      - baz=name_of_baz_secret_in_vault

This will result in two secrets being generated in the test-ns namespace. 
* secret1 will contain the keys foo and bar, assigned to these will be the values of the keyvault secrets 'name_of_foo_secret_in_vault' and 'name_of_bar_secret_in_vault'.
* secret2 will contain the key baz which will have the value of the keyvault secret 'name_of_baz_secret_in_vault'

If the name or namespace of a secret is unset then it will default to the name/namespace of the parent AzureSecrets. See the Dockerfile for more examples.

## Installation

This has been tested with Kustomize 3.5.4 (see docker file)

    go get -d github.com/devjoes/azure-secrets/
    mkdir -p ~/.config/kustomize/plugin/devjoes/v1/azuresecrets/
    go build -buildmode plugin -o ~/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so ./AzureSecrets.go

There is a Docker image [here](https://hub.docker.com/r/joeshearn/azure-secrets). You can either run this as it is, use it as a base image or copy the relevant files out of it like this:

    FROM alpine:latest
    COPY --from=joeshearn/azure-secrets /bin/kustomize /bin/kustomize
    COPY --from=joeshearn/azure-secrets /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so