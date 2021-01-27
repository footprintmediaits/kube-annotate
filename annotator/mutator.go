package annotator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/chickenzord/kube-annotate/config"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

//Patch patching operation
type Patch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)
)

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = appsv1.AddToScheme(runtimeScheme)
}

func parseBody(r *http.Request) (*admissionv1.AdmissionReview, error) {
	if r.ContentLength == 0 {
		return nil, errors.New("empty body")
	}

	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
		return nil, fmt.Errorf("invalid content type: %s", contentType)
	}

	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read body: %v", err)
	}

	result := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(data, nil, &result); err != nil {
		return nil, fmt.Errorf("cannot deserialize data to AdmissionReview: %v", err)
	}

	return &result, nil
}

func respond(review *admissionv1.AdmissionReview, response *admissionv1.AdmissionResponse) *admissionv1.AdmissionReview {
	result := &admissionv1.AdmissionReview{}
	if response != nil {
		result.Response = response
		if review.Request != nil {
			result.Response.UID = review.Request.UID
		}
	}
	return result
}

func respondWithError(review *admissionv1.AdmissionReview, err error) *admissionv1.AdmissionReview {
	return respond(review, &admissionv1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	})
}

func respondWithSkip(review *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	return respond(review, &admissionv1.AdmissionResponse{
		Allowed: true,
	})
}

func respondWithPatches(review *admissionv1.AdmissionReview, patches []Patch) *admissionv1.AdmissionReview {
	patchesBytes, err := json.Marshal(patches)
	if err != nil {
		return respondWithError(review, fmt.Errorf("cannot serialize patches: %v", err))
	}

	return respond(review, &admissionv1.AdmissionResponse{
		Allowed: true,
		Patch:   patchesBytes,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
	})
}

func createPatchFromAnnotations(base, extra map[string]string) Patch {
	if base == nil {
		return Patch{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: extra,
		}
	}

	annotations := make(map[string]string)
	for k, v := range base {
		annotations[k] = v
	}
	if extra != nil {
		for k, v := range extra {
			annotations[k] = v
		}
	}

	return Patch{
		Op:    "replace",
		Path:  "/metadata/annotations",
		Value: annotations,
	}
}

func mutate(review *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	//deserialize pod
	var pod corev1.Pod
	log.WithData(review).Debug("my log")
	if _, _, err := deserializer.Decode(review.Request.Object.Raw, nil, &pod); err != nil {
		log.WithData(review).WithError(err).Errorf("error mutating pod")
		return respondWithError(review, errors.New("cannot deserialize pod from AdmissionRequest"))
	}

	//create patches based on rules
	log.WithData(review).Debug("processing AdmissionReview")
	patches := make([]Patch, 0)
	for _, rule := range config.Rules {
		if rule.Selector.AsSelector().Matches(labels.Set(pod.Labels)) {
			patch := createPatchFromAnnotations(pod.Annotations, rule.Annotations)
			patches = append(patches, patch)
		}
	}

	if len(patches) > 0 {
		log.WithData(review).Infof("mutating Pod with %d patch(es)", len(patches))
		return respondWithPatches(review, patches)
	}

	log.Infof("skipping Pod")
	return respondWithSkip(review)
}
