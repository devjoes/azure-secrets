FROM golang AS deps
ARG RUN_INTEGRATION_TESTS=0
#  docker.exe build . --build-arg RUN_INTEGRATION_TESTS=1 --build-arg LOGIN_MANUALY="1" \
# --build-arg  AZURE_SUB_ID="00000000-0000-0000-0000-000000000000"

ENV GO111MODULE=on \
    GOOS=linux \
    GOARCH=amd64 \
    CGO_ENABLED=1

RUN mkdir -p /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/ \
    && apt update && apt install curl tar openssl sudo bash jq -y \
    && apt install postgresql-client postgresql -y \
    && apt install python3-pip -y \
    && apt install gcc libffi-dev musl-dev libssl-dev python3-dev make -y \
    && pip3 --no-cache-dir install azure-cli==2.0.81 \
    && go get sigs.k8s.io/kustomize/kustomize/v3@v3.5.4

FROM deps AS build
WORKDIR /src/
COPY . .
RUN go build -buildmode plugin -o /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/AzureSecrets.so AzureSecrets.go \
    && mkdir /root/sigs.k8s.io/kustomize/plugin -p \
    && go test AzureSecrets_test.go

FROM build AS test
ARG AZURE_TENANT_ID 
ARG AZURE_CLIENT_ID 
ARG AZURE_CLIENT_SECRET
ARG RUN_INTEGRATION_TESTS=0
ARG DISABLE_AZURE_AUTH_VALIDATION=1
ARG AZURE_SUB_ID
ARG LOGIN_MANUALY

ENV AZURE_TENANT_ID  $AZURE_TENANT_ID 
ENV AZURE_CLIENT_ID  $AZURE_CLIENT_ID 
ENV AZURE_CLIENT_SECRET $AZURE_CLIENT_SECRET
ENV RUN_INTEGRATION_TESTS $RUN_INTEGRATION_TESTS
ENV DISABLE_AZURE_AUTH_VALIDATION $DISABLE_AZURE_AUTH_VALIDATION
ENV AZURE_SUB_ID $AZURE_SUB_ID
ENV LOGIN_MANUALY $LOGIN_MANUALY

COPY example /src/example
WORKDIR /src/example

RUN sh test.sh
FROM alpine AS final
ARG AZURE_TENANT_ID 
ARG AZURE_CLIENT_ID 
ARG AZURE_CLIENT_SECRET 

COPY --from=build /root/.config /root/.config
COPY --from=build /go/bin/kustomize /bin/kustomize

ENTRYPOINT ["kustomize"]