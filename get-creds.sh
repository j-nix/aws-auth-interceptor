#!/bin/bash
go run .
echo "Exporting credentials to shell environment!"
export AWS_ACCESS_KEY_ID=$(jq -c '.Credentials.AccessKeyId' < /tmp/aws-auth | tr -d '"' | tr -d ' ')
export AWS_SECRET_ACCESS_KEY=$(jq -c '.Credentials.SecretAccessKey' < /tmp/aws-auth | tr -d '"' | tr -d ' ')
export AWS_SESSION_TOKEN=$(jq -c '.Credentials.SessionToken' < /tmp/aws-auth | tr -d '"' | tr -d ' ')
