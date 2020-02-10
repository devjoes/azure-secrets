FROM golang:alpine AS deps
ARG RUN_INTEGRATION_TESTS=0

ENV GO111MODULE=on \
    GOOS=linux \
    GOARCH=amd64 \
    CGO_ENABLED=1

RUN apk add git gcc g++ \
    && mkdir -p /root/.config/kustomize/plugin/devjoes/v1/azuresecrets/ \
    && apk add --no-cache curl tar openssl sudo bash jq \
    && apk --update --no-cache add postgresql-client postgresql \
    && apk add py-pip \
    && apk add --virtual=build gcc libffi-dev musl-dev openssl-dev python-dev make \
    && pip --no-cache-dir install azure-cli==2.0.81 \
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

COPY example /src/example
WORKDIR /src/example

RUN if [ "$RUN_INTEGRATION_TESTS" == "1" ]; then sh test.sh; fi

FROM alpine AS final
ARG AZURE_TENANT_ID 
ARG AZURE_CLIENT_ID 
ARG AZURE_CLIENT_SECRET 

COPY --from=build /root/.config /root/.config
COPY --from=build /go/bin/kustomize /bin/kustomize

ENTRYPOINT ["kustomize"]