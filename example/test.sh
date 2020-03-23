#!/bin/sh

loginWithSp(){
  if [[ -z $AZURE_TENANT_ID ]] || [[ -z $AZURE_CLIENT_ID ]] || [[ -z $AZURE_CLIENT_SECRET ]]; then
      echo "Please set AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET"
      exit 11
  fi

  echo az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID"
  az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID" || exit 12
  az account show --sdk-auth
}

loginManualy() {
  if [ -z $AZURE_SUB_ID ]; then
    echo "Please set \$AZURE_SUB_ID to the subscripton where you want to create test keyvaults"
    exit 1
  fi
  echo "Press enter to log in"
  read  
  az login
  az account set --subscription "$AZURE_SUB_ID"
  az ad sp create-for-rbac | tee sp.json
  export AZURE_CLIENT_ID="$(jq -r '.appId' sp.json)"
  export AZURE_CLIENT_SECRET="$(jq -r '.password' sp.json)"
  export AZURE_TENANT_ID="$(jq -r '.tenant' sp.json)"
  export DELETE_SP=1
}

if [ "$RUN_INTEGRATION_TESTS" != "1" ]; then
  echo "Not running integration tests because \$RUN_INTEGRATION_TESTS is '$RUN_INTEGRATION_TESTS'"
  exit 0
fi

echo "Testing offline mode"
export AZURE_SECRETS_OFFLINE_TESTING_MODE=1
export AZURE_SECRETS_OFFLINE_TESTING_MODE_WARN_SECONDS=0
kustomize build . --enable_alpha_plugins || exit 1
export AZURE_SECRETS_OFFLINE_TESTING_MODE=

if [ "$LOGIN_MANUALY" == "1" ]; then
  loginManualy
else
  loginWithSp
fi

NOW=`date +%s`
KEYVAULT="kustazsecrets-$NOW"
echo "Keyvault is $KEYVAULT in rg-$KEYVAULT"

cleanup() {
  if [ "$DELETE_SP" == "1" ]; then
    echo "Deleting SP $AZURE_CLIENT_ID"
    az ad sp delete --id "$AZURE_CLIENT_ID"
  fi
  
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
az keyvault secret set --vault-name "$KEYVAULT" --name "test-secret3" --value "c3Nzc3NoaCEgSSdtIGEgYmFzZTY0IGVuY29kZWQgc2VjcmV0"
az keyvault set-policy --name "$KEYVAULT" --resource-group "rg-$KEYVAULT" --spn "$AZURE_CLIENT_ID" --secret-permissions "get"
sed -i "s/TestKeyVault/$KEYVAULT/g" azure_secrets.yaml
cat azure_secrets.yaml

az account show
kustomize build . --enable_alpha_plugins || exit 1
