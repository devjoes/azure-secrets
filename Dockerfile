FROM golang:alpine AS build

ENV GO111MODULE=on \
    GOOS=linux \
    GOARCH=amd64 \
    CGO_ENABLED=1

RUN apk add git gcc g++ \
    && mkdir -p /root/.config/kustomize/plugin/devjoes/v1/azuresecretsgenerator/ \
    && go get sigs.k8s.io/kustomize/kustomize/v3@v3.5.4 \
    && apk add --no-cache curl tar openssl sudo bash jq \
    && apk --update --no-cache add postgresql-client postgresql \
    && apk add py-pip \
    && apk add --virtual=build gcc libffi-dev musl-dev openssl-dev python-dev make \
    && pip --no-cache-dir install azure-cli==2.0.81

COPY plugin /src/plugin
WORKDIR /src/plugin/devjoes/v1/azuresecretsgenerator

RUN go build -buildmode plugin -o /root/.config/kustomize/plugin/devjoes/v1/azuresecretsgenerator/AzureSecretsGenerator.so AzureSecretsGenerator.go \
&& chmod +x /root/.config/kustomize/plugin/devjoes/v1/azuresecretsgenerator/AzureSecretsGenerator.so \
&& go test AzureSecretsGenerator_test.go

COPY example /src/example
WORKDIR /src/example

ARG AZURE_TENANT_ID 
ARG AZURE_CLIENT_ID 
ARG AZURE_CLIENT_SECRET 

RUN sh test.sh

FROM alpine AS final
COPY --from=build /root/.config /root/.config
COPY --from=build /go/bin/kustomize /bin/kustomize

CMD ["sh"]