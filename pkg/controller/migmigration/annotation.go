package migmigration

import (
	"context"
	"strings"

	migapi "github.com/konveyor/mig-controller/pkg/apis/migration/v1alpha1"
	"github.com/konveyor/mig-controller/pkg/gvk"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Velero Plugin Annotations
const (
	StageOrFinalAnnotation   = "openshift.io/migrate-copy-phase"   // (stage|final)
	PvActionAnnotation       = "openshift.io/migrate-type"         // (move|copy)
	PvStorageClassAnnotation = "openshift.io/target-storage-class" // storageClassName
	PvAccessModeAnnotation   = "openshift.io/target-access-mode"   // accessMode
	PvCopyMethodAnnotation   = "openshift.io/copy-method"          // (filesystem|snapshot)
	PvVerifyAnnotation       = "openshift.io/verify"               // (true|false)
	QuiesceAnnotation        = "openshift.io/migrate-quiesce-pods" // (true|false)
	QuiesceNodeSelector      = "migration.openshift.io/quiesceDaemonSet"
	SuspendAnnotation        = "migration.openshift.io/preQuiesceSuspend"
	ReplicasAnnotation       = "migration.openshift.io/preQuiesceReplicas"
	NodeSelectorAnnotation   = "migration.openshift.io/preQuiesceNodeSelector"
)

// Restic Annotations
const (
	ResticPvBackupAnnotation = "backup.velero.io/backup-volumes" // comma-separated list of volume names
	ResticPvVerifyAnnotation = "backup.velero.io/verify-volumes" // comma-separated list of volume names
)

// Labels.
const (
	// Resources included in the stage backup.
	// Referenced by the Backup.LabelSelector. The value is the Task.UID().
	IncludedInStageBackupLabel = "migration-included-stage-backup"
	// Application pods requiring restic/stage backups.
	// Used to create stage pods. The value is the Task.UID()
	ApplicationPodLabel = "migration-application-pod"
	// Designated as an `initial` Backup.
	// The value is the Task.UID().
	InitialBackupLabel = "migration-initial-backup"
	// Designated as an `stage` Backup.
	// The value is the Task.UID().
	StageBackupLabel = "migration-stage-backup"
	// Designated as an `stage` Restore.
	// The value is the Task.UID().
	StageRestoreLabel = "migration-stage-restore"
	// Designated as a `final` Restore.
	// The value is the Task.UID().
	FinalRestoreLabel = "migration-final-restore"
	// Identifies the resource as migrated by us
	// for easy search or application rollback.
	// The value is the Task.UID().
	MigratedByLabel = "migration.openshift.io/migrated-by" // (migmigration UID)
)

// Set of Service Accounts.
// Keyed by namespace (name) with value of map keyed by SA name.
type ServiceAccounts map[string]map[string]bool

// Add annotations and labels.
// The PvActionAnnotation annotation is added to PV & PVC as needed by the velero plugin.
// The PvStorageClassAnnotation annotation is added to PVC as needed by the velero plugin.
// The PvAccessModeAnnotation annotation is added to PVC as needed by the velero plugin.
// The ResticPvBackupAnnotation is added to Pods as needed by Restic.
// The ResticPvVerifyAnnotation is added to Pods as needed by Restic.
// The IncludedInStageBackupLabel label is added to Pods, PVs, PVCs and is referenced
// by the velero.Backup label selector.
// The IncludedInStageBackupLabel label is added to Namespaces to prevent the
// velero.Backup from being empty which causes the Restore to fail.
func (t *Task) annotateStageResources() error {
	client, err := t.getSourceClient()
	if err != nil {
		log.Trace(err)
		return err
	}
	// Namespaces
	err = t.labelNamespaces(client)
	if err != nil {
		log.Trace(err)
		return err
	}
	// PVs and PVCs
	err = t.annotatePVsAndPVCs(client)
	if err != nil {
		log.Trace(err)
		return err
	}
	// Pods
	serviceAccounts, err := t.annotatePods(client)
	if err != nil {
		log.Trace(err)
		return err
	}
	// Service accounts used by stage pods.
	err = t.labelServiceAccounts(client, serviceAccounts)
	if err != nil {
		log.Trace(err)
		return err
	}

	return nil
}

// Gets a list of restic volumes and restic verify volumes for a pod
func getResticVolumes(pod corev1.Pod, t *Task) ([]string, []string) {
	volumes := []string{}
	verifyVolumes := []string{}
	pvs := t.getPVs()

	for _, pv := range pod.Spec.Volumes {
		claim := pv.VolumeSource.PersistentVolumeClaim
		if claim == nil {
			continue
		}
		pvAction := findPVAction(pvs, pv.Name)
		if pvAction == migapi.PvCopyAction {
			// Add to Restic volume list if copyMethod is "filesystem"
			if findPVCopyMethod(pvs, pv.Name) == migapi.PvFilesystemCopyMethod {
				volumes = append(volumes, pv.Name)
				if findPVVerify(pvs, pv.Name) {
					verifyVolumes = append(verifyVolumes, pv.Name)
				}
			}
		}
	}

	return volumes, verifyVolumes
}

// Add label to namespaces
func (t *Task) labelNamespaces(client k8sclient.Client) error {
	for _, ns := range t.sourceNamespaces() {
		namespace := corev1.Namespace{}
		err := client.Get(
			context.TODO(),
			k8sclient.ObjectKey{
				Name: ns,
			},
			&namespace)
		if err != nil {
			log.Trace(err)
			return err
		}
		if namespace.Labels == nil {
			namespace.Labels = make(map[string]string)
		}
		namespace.Labels[IncludedInStageBackupLabel] = t.UID()
		err = client.Update(context.TODO(), &namespace)
		if err != nil {
			log.Trace(err)
			return err
		}
		log.Info(
			"NS annotations/labels added.",
			"name",
			namespace.Name)
	}
	return nil
}

// Add annotations and labels to Pods.
// The ResticPvBackupAnnotation is added to Pods as needed by Restic.
// The ResticPvVerifyAnnotation is added to Pods as needed by Restic.
// The IncludedInStageBackupLabel label is added to Pods and is referenced
// by the velero.Backup label selector.
// Returns a set of referenced service accounts.
func (t *Task) annotatePods(client k8sclient.Client) (ServiceAccounts, error) {
	serviceAccounts := ServiceAccounts{}
	list := corev1.PodList{}
	// include stage pods only
	options := k8sclient.MatchingLabels(t.Owner.GetCorrelationLabels())
	err := client.List(context.TODO(), options, &list)
	if err != nil {
		log.Trace(err)
		return nil, err
	}
	for _, pod := range list.Items {
		annotatePod(pod, serviceAccounts, t)
		// Update
		err := client.Update(context.TODO(), &pod)
		if err != nil {
			log.Trace(err)
			return nil, err
		}
		log.Info(
			"Pod annotations/labels added.",
			"ns",
			pod.Namespace,
			"name",
			pod.Name)
	}

	return serviceAccounts, nil
}

// Add annotations and labels to a single pod
// The ResticPvBackupAnnotation is added to Pod as needed by Restic.
// The ResticPvVerifyAnnotation is added to Pod as needed by Restic.
// The IncludedInStageBackupLabel label is added to Pod and is referenced
// by the velero.Backup label selector.
// This is used when creating dedicated stage pods
// client.Update is not called on the pod since at this point, these are new not-yet-created
// pods and duplicate stage pods may still be removed from the list
func annotatePod(pod corev1.Pod, serviceAccounts ServiceAccounts, t *Task) {
	volumes, verifyVolumes := getResticVolumes(pod, t)
	// Restic annotation used to specify volumes.
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[ResticPvBackupAnnotation] = strings.Join(volumes, ",")
	pod.Annotations[ResticPvVerifyAnnotation] = strings.Join(verifyVolumes, ",")
	// Add label used by stage backup label selector.
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[ApplicationPodLabel] = t.UID()
	sa := pod.Spec.ServiceAccountName
	names, found := serviceAccounts[pod.Namespace]
	if !found {
		serviceAccounts[pod.Namespace] = map[string]bool{sa: true}
	} else {
		names[sa] = true
	}
}

// Add annotations and labels to PVs and PVCs.
// The PvActionAnnotation annotation is added to PVs and PVCs as needed by the velero plugin.
// The PvStorageClassAnnotation annotation is added to PVs and PVCs as needed by the velero plugin.
// The IncludedInStageBackupLabel label is added to PVs and PVCs and is referenced
// by the velero.Backup label selector.
// The PvAccessModeAnnotation annotation is added to PVC as needed by the velero plugin.
// The PvCopyMethodAnnotation annotation is added to PVCs as needed by the velero plugin.
// The PvVerifyAnnotation annotation is added to PVCs as needed by the velero plugin.
func (t *Task) annotatePVsAndPVCs(client k8sclient.Client) error {
	pvs := t.getPVs()
	for _, pv := range pvs.List {
		pvResource := corev1.PersistentVolume{}
		err := client.Get(
			context.TODO(),
			k8sclient.ObjectKey{
				Name: pv.Name,
			},
			&pvResource)
		if err != nil {
			log.Trace(err)
			return err
		}
		if pvResource.Annotations == nil {
			pvResource.Annotations = make(map[string]string)
		}
		// PV action (move|copy) needed by the velero plugin.
		pvResource.Annotations[PvActionAnnotation] = pv.Selection.Action
		// PV storageClass annotation needed by the velero plugin.
		pvResource.Annotations[PvStorageClassAnnotation] = pv.Selection.StorageClass
		// Add label used by stage backup label selector.
		if pvResource.Labels == nil {
			pvResource.Labels = make(map[string]string)
		}
		pvResource.Labels[IncludedInStageBackupLabel] = t.UID()
		// Update
		err = client.Update(context.TODO(), &pvResource)
		if err != nil {
			log.Trace(err)
			return err
		}
		log.Info(
			"PV annotations/labels added.",
			"name",
			pv.Name)

		pvcResource := corev1.PersistentVolumeClaim{}
		err = client.Get(
			context.TODO(),
			k8sclient.ObjectKey{
				Namespace: pv.PVC.Namespace,
				Name:      pv.PVC.Name,
			},
			&pvcResource)
		if err != nil {
			log.Trace(err)
			return err
		}
		if pvcResource.Annotations == nil {
			pvcResource.Annotations = make(map[string]string)
		}
		// PV action (move|copy) needed by the velero plugin.
		pvcResource.Annotations[PvActionAnnotation] = pv.Selection.Action
		// Add label used by stage backup label selector.
		if pvcResource.Labels == nil {
			pvcResource.Labels = make(map[string]string)
		}
		pvcResource.Labels[IncludedInStageBackupLabel] = t.UID()
		if pv.Selection.Action == migapi.PvCopyAction {
			pvcResource.Annotations[PvCopyMethodAnnotation] = pv.Selection.CopyMethod
			if pv.Selection.CopyMethod == migapi.PvFilesystemCopyMethod && pv.Selection.Verify {
				pvcResource.Annotations[PvVerifyAnnotation] = "true"
			}
			// PV storageClass annotation needed by the velero plugin.
			pvcResource.Annotations[PvStorageClassAnnotation] = pv.Selection.StorageClass
			// PV accessMode annotation needed by the velero plugin, if present on the PV.
			if pv.Selection.AccessMode != "" {
				pvcResource.Annotations[PvAccessModeAnnotation] = string(pv.Selection.AccessMode)
			}
		}
		// Update
		err = client.Update(context.TODO(), &pvcResource)
		if err != nil {
			log.Trace(err)
			return err
		}
		log.Info(
			"PVC annotations/labels added.",
			"ns",
			pv.PVC.Namespace,
			"name",
			pv.PVC.Name)
	}

	return nil
}

// Add label to service accounts.
func (t *Task) labelServiceAccounts(client k8sclient.Client, serviceAccounts ServiceAccounts) error {
	return labelServiceAccountsInternal(client, serviceAccounts, t.sourceNamespaces(), t.UID())
}

func labelServiceAccountsInternal(client k8sclient.Client, serviceAccounts ServiceAccounts, namespaces []string, uid string) error {
	for _, ns := range namespaces {
		names, found := serviceAccounts[ns]
		if !found {
			continue
		}
		list := corev1.ServiceAccountList{}
		options := k8sclient.InNamespace(ns)
		err := client.List(context.TODO(), options, &list)
		if err != nil {
			log.Trace(err)
			return err
		}
		for _, sa := range list.Items {
			if _, found := names[sa.Name]; !found {
				continue
			}
			if sa.Labels == nil {
				sa.Labels = make(map[string]string)
			}
			sa.Labels[IncludedInStageBackupLabel] = uid
			err = client.Update(context.TODO(), &sa)
			if err != nil {
				log.Trace(err)
				return err
			}
			log.Info(
				"SA annotations/labels added.",
				"ns",
				sa.Namespace,
				"name",
				sa.Name)
		}
	}

	return nil
}

// Delete temporary annotations and labels added.
func (t *Task) deleteAnnotations() error {
	clients, namespaceList, err := t.getBothClientsWithNamespaces()
	if err != nil {
		log.Trace(err)
		return err
	}

	for i, client := range clients {
		err = t.deletePVCAnnotations(client, namespaceList[i])
		if err != nil {
			log.Trace(err)
			return err
		}
		err = t.deletePVAnnotations(client)
		if err != nil {
			log.Trace(err)
			return err
		}
		err = t.deletePodAnnotations(client, namespaceList[i])
		if err != nil {
			log.Trace(err)
			return err
		}
		err = t.deleteNamespaceLabels(client, namespaceList[i])
		if err != nil {
			log.Trace(err)
			return err
		}
		err = t.deleteServiceAccountLabels(client)
		if err != nil {
			log.Trace(err)
			return err
		}
	}

	return nil
}

// Delete Pod stage annotations and labels.
func (t *Task) deletePodAnnotations(client k8sclient.Client, namespaceList []string) error {
	for _, ns := range namespaceList {
		options := k8sclient.InNamespace(ns)
		podList := corev1.PodList{}
		err := client.List(context.TODO(), options, &podList)
		if err != nil {
			log.Trace(err)
			return err
		}
		for _, pod := range podList.Items {
			if !pod.ObjectMeta.DeletionTimestamp.IsZero() {
				continue
			}
			needsUpdate := false
			if pod.Annotations != nil {
				if _, found := pod.Annotations[ResticPvBackupAnnotation]; found {
					delete(pod.Annotations, ResticPvBackupAnnotation)
					needsUpdate = true
				}
				if _, found := pod.Annotations[ResticPvVerifyAnnotation]; found {
					delete(pod.Annotations, ResticPvVerifyAnnotation)
					needsUpdate = true
				}
			}
			if pod.Labels != nil {
				if _, found := pod.Labels[IncludedInStageBackupLabel]; found {
					delete(pod.Labels, IncludedInStageBackupLabel)
					needsUpdate = true
				}
				if _, found := pod.Labels[ApplicationPodLabel]; found {
					delete(pod.Labels, ApplicationPodLabel)
					needsUpdate = true
				}
			}
			if !needsUpdate {
				continue
			}
			err = client.Update(context.TODO(), &pod)
			if err != nil {
				log.Trace(err)
				return err
			}
			log.Info(
				"Pod annotations/labels removed.",
				"ns",
				pod.Namespace,
				"name",
				pod.Name)
		}
	}

	return nil
}

// Delete stage label from namespaces
func (t *Task) deleteNamespaceLabels(client k8sclient.Client, namespaceList []string) error {
	for _, ns := range namespaceList {
		namespace := corev1.Namespace{}
		err := client.Get(
			context.TODO(),
			k8sclient.ObjectKey{
				Name: ns,
			},
			&namespace)
		// Check if namespace doesn't exist. This will happpen during prepare phase
		// since destination cluster doesn't have the migrated namespaces yet
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			log.Trace(err)
			return err
		}
		delete(namespace.Labels, IncludedInStageBackupLabel)
		err = client.Update(context.TODO(), &namespace)
		if err != nil {
			log.Trace(err)
			return err
		}
	}
	return nil
}

// deleteLabels will delete all migration.openshift.io/migrated-by labels
// from the application upon successful completion
func (t *Task) deleteLabels() error {
	client, GVRs, err := gvk.GetGVRsForCluster(t.PlanResources.DestMigCluster, t.Client)
	if err != nil {
		log.Trace(err)
		return err
	}

	listOptions := k8sclient.MatchingLabels(map[string]string{
		MigratedByLabel: string(t.Owner.UID),
	}).AsListOptions()

	for _, gvr := range GVRs {
		for _, ns := range t.destinationNamespaces() {
			list, err := client.Resource(gvr).Namespace(ns).List(*listOptions)
			if err != nil {
				log.Trace(err)
				return err
			}
			for _, r := range list.Items {
				labels := r.GetLabels()
				delete(labels, MigratedByLabel)
				r.SetLabels(labels)
				_, err = client.Resource(gvr).Namespace(ns).Update(&r, metav1.UpdateOptions{})
				if err != nil {
					// The part touches the application on the destination side, fail safe to ensure
					// this won't block the migration
					if k8serror.IsMethodNotSupported(err) {
						continue
					}
					log.Trace(err)
					return err
				}
			}
		}
	}

	return nil
}

// Delete PVC stage annotations and labels.
func (t *Task) deletePVCAnnotations(client k8sclient.Client, namespaceList []string) error {
	for _, ns := range namespaceList {
		options := k8sclient.InNamespace(ns)
		pvcList := corev1.PersistentVolumeClaimList{}
		err := client.List(context.TODO(), options, &pvcList)
		if err != nil {
			log.Trace(err)
			return err
		}
		for _, pvc := range pvcList.Items {
			if pvc.Spec.VolumeName == "" {
				continue
			}
			needsUpdate := false
			if pvc.Annotations != nil {
				if _, found := pvc.Annotations[PvActionAnnotation]; found {
					delete(pvc.Annotations, PvActionAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[PvStorageClassAnnotation]; found {
					delete(pvc.Annotations, PvStorageClassAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[PvAccessModeAnnotation]; found {
					delete(pvc.Annotations, PvAccessModeAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[PvCopyMethodAnnotation]; found {
					delete(pvc.Annotations, PvCopyMethodAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[PvVerifyAnnotation]; found {
					delete(pvc.Annotations, PvVerifyAnnotation)
					needsUpdate = true
				}
			}
			if pvc.Labels != nil {
				if _, found := pvc.Labels[IncludedInStageBackupLabel]; found {
					delete(pvc.Labels, IncludedInStageBackupLabel)
					needsUpdate = true
				}
			}
			if !needsUpdate {
				continue
			}
			err = client.Update(context.TODO(), &pvc)
			if err != nil {
				log.Trace(err)
				return err
			}
			log.Info(
				"PVC annotations/labels removed.",
				"ns",
				pvc.Namespace,
				"name",
				pvc.Name)
		}
	}

	return nil
}

// Delete PV stage annotations and labels.
func (t *Task) deletePVAnnotations(client k8sclient.Client) error {
	labels := map[string]string{
		IncludedInStageBackupLabel: t.UID(),
	}
	options := k8sclient.MatchingLabels(labels)
	pvList := corev1.PersistentVolumeList{}
	err := client.List(context.TODO(), options, &pvList)
	if err != nil {
		log.Trace(err)
		return err
	}
	for _, pv := range pvList.Items {
		delete(pv.Labels, IncludedInStageBackupLabel)
		delete(pv.Annotations, PvActionAnnotation)
		delete(pv.Annotations, PvStorageClassAnnotation)
		err = client.Update(context.TODO(), &pv)
		if err != nil {
			log.Trace(err)
			return err
		}
		log.Info(
			"PV annotations/labels removed.",
			"name",
			pv.Name)
	}

	return nil
}

// Delete service account labels.
func (t *Task) deleteServiceAccountLabels(client k8sclient.Client) error {
	labels := map[string]string{
		IncludedInStageBackupLabel: t.UID(),
	}
	options := k8sclient.MatchingLabels(labels)
	pvList := corev1.ServiceAccountList{}
	err := client.List(context.TODO(), options, &pvList)
	if err != nil {
		log.Trace(err)
		return err
	}
	for _, sa := range pvList.Items {
		delete(sa.Labels, IncludedInStageBackupLabel)
		err = client.Update(context.TODO(), &sa)
		if err != nil {
			log.Trace(err)
			return err
		}
		log.Info(
			"SA annotations/labels removed.",
			"ns",
			sa.Namespace,
			"name",
			sa.Name)
	}

	return nil
}
