/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package registrar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// WorkloadOAuthSecretKeyClientID is the Secret data key for the OAuth client_id file.
	WorkloadOAuthSecretKeyClientID = "client-id.txt"
	// WorkloadOAuthSecretKeyClientSecret is the Secret data key for the OAuth client_secret file.
	WorkloadOAuthSecretKeyClientSecret = "client-secret.txt"

	LabelManagedBy        = "app.kubernetes.io/managed-by"
	ValueManagedByWebhook = "kagenti-webhook"
	LabelClientIDHash     = "kagenti.io/client-id-sha256"
	LabelWorkload         = "kagenti.io/workload"
)

// WorkloadOAuthSecretName returns a deterministic Secret name for a workload OAuth client.
func WorkloadOAuthSecretName(namespace, workloadName, clientID string) string {
	h := sha256.Sum256([]byte(namespace + "\x00" + workloadName + "\x00" + clientID))
	return fmt.Sprintf("kagenti-oauth-%s", hex.EncodeToString(h[:8]))
}

func clientIDLabelHash(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(sum[:8])
}

// EnsureWorkloadOAuthSecret creates or updates the workload namespace Secret with
// OAuth client credentials. It is safe for concurrent admission of the same workload.
func EnsureWorkloadOAuthSecret(ctx context.Context, c client.Client, namespace, secretName, workloadName, clientID string, res *Result) error {
	if res == nil {
		return fmt.Errorf("registrar result is nil")
	}
	idHash := clientIDLabelHash(clientID)

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManagedBy:    ValueManagedByWebhook,
				LabelClientIDHash: idHash,
				LabelWorkload:     sanitizeLabel(workloadName),
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			WorkloadOAuthSecretKeyClientID:     res.ClientID,
			WorkloadOAuthSecretKeyClientSecret: res.ClientSecret,
		},
	}

	var existing corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: secretName}
	err := c.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return createOrRetryOAuthSecret(ctx, c, desired, namespace, secretName, res, idHash)
	}
	if err != nil {
		return err
	}

	// Another object with same name: verify we own it for this client_id.
	if existing.Labels[LabelManagedBy] != ValueManagedByWebhook {
		return fmt.Errorf("secret %s/%s exists but is not managed by kagenti-webhook", namespace, secretName)
	}
	if existing.Labels[LabelClientIDHash] != "" && existing.Labels[LabelClientIDHash] != idHash {
		return fmt.Errorf("secret %s/%s exists for a different client_id", namespace, secretName)
	}

	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	existing.Data[WorkloadOAuthSecretKeyClientID] = []byte(res.ClientID)
	existing.Data[WorkloadOAuthSecretKeyClientSecret] = []byte(res.ClientSecret)
	existing.Labels = mergeLabels(existing.Labels, desired.Labels)

	return c.Update(ctx, &existing)
}

func createOrRetryOAuthSecret(ctx context.Context, c client.Client, desired *corev1.Secret, namespace, secretName string, res *Result, idHash string) error {
	if err := c.Create(ctx, desired); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		var existing corev1.Secret
		key := types.NamespacedName{Namespace: namespace, Name: secretName}
		if err := c.Get(ctx, key, &existing); err != nil {
			return err
		}
		if existing.Labels[LabelManagedBy] != ValueManagedByWebhook {
			return fmt.Errorf("secret %s/%s exists but is not managed by kagenti-webhook", namespace, secretName)
		}
		if existing.Labels[LabelClientIDHash] != "" && existing.Labels[LabelClientIDHash] != idHash {
			return fmt.Errorf("secret %s/%s exists for a different client_id", namespace, secretName)
		}
		if existing.Data == nil {
			existing.Data = make(map[string][]byte)
		}
		existing.Data[WorkloadOAuthSecretKeyClientID] = []byte(res.ClientID)
		existing.Data[WorkloadOAuthSecretKeyClientSecret] = []byte(res.ClientSecret)
		existing.Labels = mergeLabels(existing.Labels, desired.Labels)
		return c.Update(ctx, &existing)
	}
	return nil
}

func mergeLabels(cur, want map[string]string) map[string]string {
	if cur == nil {
		return want
	}
	for k, v := range want {
		cur[k] = v
	}
	return cur
}

func sanitizeLabel(s string) string {
	if len(s) <= 63 {
		return s
	}
	return s[:63]
}
