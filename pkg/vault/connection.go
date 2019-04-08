// Copyright 2019 RedHat
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vault

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/hashicorp/vault/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("pagerduty_vault")

func saveSecret(path string, value string) error {
	os.Remove(path)
	file, err := os.Create(path)
	if err != nil {
		log.Error(err, "Failed to create temp file")
		return err
	}
	_, err = file.WriteString(value)
	if err != nil {
		log.Error(err, "Failed to write to temp file")
		return err
	}

	return nil
}

func getDataKey(data map[string][]byte, key string) (string, error) {
	if _, ok := data[key]; !ok {
		errorStr := fmt.Sprintf("%v is not set.", key)
		return "", errors.New(errorStr)
	}
	retString := string(data[key])
	if len(retString) <= 0 {
		errorStr := fmt.Sprintf("%v is empty", key)
		return "", errors.New(errorStr)
	}
	return retString, nil
}

// Data describes a struct that we will use to pass data from vault to other functions
type Data struct {
	Namespace  string
	SecretName string
	Path       string
	Property   string
	URL        string
	Token      string
	Mount      string
	Key        string
}

func (data *Data) queryVault() (string, error) {
	vaultFullPath := fmt.Sprintf("%v/data/%v", data.Mount, data.Path)

	client, err := api.NewClient(&api.Config{
		Address: string(data.URL),
	})
	if err != nil {
		return "", err
	}
	client.SetToken(string(data.Token))

	vault, err := client.Logical().Read(vaultFullPath)
	if err != nil {
		return "", err
	}

	secret, ok := vault.Data["data"].(map[string]interface{})
	if !ok {
		return "", errors.New("Error parsing secret data")
	}

	if len(vault.Warnings) > 0 {
		for i := len(vault.Warnings) - 1; i >= 0; i-- {
			log.Info(vault.Warnings[i])
		}
	}

	if len(vault.Data) == 0 {
		return "", errors.New("Vault data is empty")
	}

	for propName, propValue := range secret {
		if propName == data.Property {
			value := fmt.Sprintf("%v", propValue)
			if len(value) <= 0 {
				return "", errors.New(data.Property + " is empty")
			}
			return value, nil
		}
	}

	return "", errors.New(data.Property + " not set in vault")
}

// GetVaultSecret Gets a designed token from vault. Vault creds are stored in a k8s secret
func (data *Data) GetVaultSecret(osc client.Client) (string, error) {
	vaultConfig := &corev1.Secret{}

	err := osc.Get(context.TODO(), types.NamespacedName{Namespace: data.Namespace, Name: data.SecretName}, vaultConfig)
	if err != nil {
		return "", err
	}

	data.URL, err = getDataKey(vaultConfig.Data, "VAULT_URL")
	if err != nil {
		return "", err
	}

	data.Token, err = getDataKey(vaultConfig.Data, "VAULT_TOKEN")
	if err != nil {
		return "", err
	}

	data.Mount, err = getDataKey(vaultConfig.Data, "VAULT_MOUNT")
	if err != nil {
		return "", err
	}

	data.Key, err = getDataKey(vaultConfig.Data, "VAULT_KEY")
	if err != nil {
		return "", err
	}

	data.Property, err = getDataKey(vaultConfig.Data, "VAULT_PROPERTY")
	if err != nil {
		return "", err
	}

	data.Path, err = getDataKey(vaultConfig.Data, "VAULT_PATH")
	if err != nil {
		return "", err
	}

	tempFilePath := fmt.Sprintf("/tmp/%v-%v", data.Mount, data.Property)
	tempFile, err := os.Stat(tempFilePath)
	if os.IsNotExist(err) || tempFile.ModTime().Before(time.Now().Add(time.Hour*time.Duration(-6))) {
		secret, err := data.queryVault()
		if err != nil {
			return "", err
		}
		err = saveSecret(tempFilePath, secret)
		if err != nil {
			log.Error(err, "Failed to save secret")
			return secret, nil
		}
	}

	fileDat, err := ioutil.ReadFile(tempFilePath)
	if err != nil {
		log.Error(err, "Failed to read file - removing")
		os.Remove(tempFilePath)
		return data.queryVault()
	}

	return string(fileDat), nil
}