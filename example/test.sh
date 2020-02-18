#!/bin/sh

if [ "$RUN_INTEGRATION_TESTS" != "1" ]; then
  echo "Not running integration tests because \$RUN_INTEGRATION_TESTS is '$RUN_INTEGRATION_TESTS'"
  exit 0
fi

if [[ -z $AZURE_TENANT_ID ]] || [[ -z $AZURE_CLIENT_ID ]] || [[ -z $AZURE_CLIENT_SECRET ]]; then
    echo "Please set AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET"
    exit 11
fi

echo az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID"
az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID" || exit 12
az account show --sdk-auth

NOW=`date +%s`
KEYVAULT="kustazsecrets-$NOW"
echo "Keyvault is $KEYVAULT in rg-$KEYVAULT"

cleanup() {
    echo "Deleting rg-$KEYVAULT"
  az group delete --name "rg-$KEYVAULT" --yes
  rm -Rf ~/.azure
}
trap cleanup EXIT

set -e

az group create -n "rg-$KEYVAULT" --location "North Europe"
az keyvault create --name "$KEYVAULT" --resource-group "rg-$KEYVAULT" --location "North Europe"
az keyvault secret set --vault-name "$KEYVAULT" --name "test-secret1" --value "SEC1 Super secret password!!"
az keyvault secret set --vault-name "$KEYVAULT" --name "test-secret2" --value "SEC2 A connection string or something? ...Nowhere near as secret as the password :( does have funny chars though!! :? "
sed -i "s/TestKeyVault/$KEYVAULT/g" azure_secrets.yaml
cat azure_secrets.yaml

kustomize build . --enable_alpha_plugins || exit 1
