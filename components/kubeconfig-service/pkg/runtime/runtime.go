package runtime

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/avast/retry-go"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	rbacv1helpers "k8s.io/kubernetes/pkg/apis/rbac/v1"
)

type SAInfo struct {
	ServiceAccountName     string
	ClusterRoleName        string
	ClusterRoleAggrLabel   string
	ClusterRoleRulesName   string
	ClusterRoleBindingName string
	Namespace              string
	SecretName             string
	TenantID               string
}

const SA = "SA"
const ClusterRole = "ClusterRole"
const ClusterRoleBinding = "ClusterRoleBinding"
const Namespace = "kube-system"
const RUNTIME_ADMIN = "runtimeAdmin"
const RUNTIME_OPERATOR = "runtimeOperator"
const ServiceAccount = "ServiceAccount"
const Token = "token"

var L2L3OperatorPolicyRule = map[string][]rbacv1.PolicyRule{
	RUNTIME_ADMIN: {
		rbacv1helpers.NewRule("*").Groups("*").Resources("*").RuleOrDie(),
		rbacv1helpers.NewRule("*").URLs("*").RuleOrDie(),
	},
	RUNTIME_OPERATOR: {
		rbacv1helpers.NewRule("get", "list", "watch").Groups("*").Resources("*").RuleOrDie(),
		rbacv1helpers.NewRule("get", "list", "watch").URLs("*").RuleOrDie(),
	},
}

var L2L3OperatorAggregationRule = map[string][]metav1.LabelSelector{
	RUNTIME_ADMIN: {
		{
			MatchLabels: map[string]string{
				"rbac.authorization.k8s.io/aggregate-to-admin": "true",
			},
		},
	},
	RUNTIME_OPERATOR: {
		{
			MatchLabels: map[string]string{
				"rbac.authorization.k8s.io/aggregate-to-edit": "true",
			},
		},
	},
}

type RollbackE struct {
	Data []string
}
type RuntimeClient struct {
	K8s               kubernetes.Interface
	KcpK8s            kubernetes.Interface
	User              SAInfo
	L2L3OperatiorRole string
	RollbackE         RollbackE
}

func NewRuntimeClient(kubeConfig []byte, userID string, L2L3OperatiorRole string, tenant string) (*RuntimeClient, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeConfig))
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	coreClientset, err := GetK8sClient()
	if err != nil {
		return nil, err
	}

	user := SAInfo{
		ServiceAccountName:     userID,
		ClusterRoleName:        userID,
		ClusterRoleAggrLabel:   fmt.Sprintf("rbac.authorization.k8s.io/aggregate-to-%s", userID),
		ClusterRoleRulesName:   fmt.Sprintf("%s-rules", userID),
		ClusterRoleBindingName: userID,
		Namespace:              Namespace,
		TenantID:               tenant,
	}
	RollbackE := RollbackE{}
	return &RuntimeClient{clientset, coreClientset, user, L2L3OperatiorRole, RollbackE}, nil
}

// kubeconfig access runtime, create sa and clusterrole and clusterrolebinding according to userID and l2L3OperatiorRole
func (rtc *RuntimeClient) Run() (string, error) {
	var resultE error
	defer func() {
		if err := rtc.Cleaner(); err != nil {
			resultE = errors.Wrapf(err, "while Cleaner")
		}
	}()

	err := rtc.createServiceAccount()
	if err != nil {
		return "", errors.Wrapf(err, "while createServiceAccount %s in %s", rtc.User.ServiceAccountName, rtc.User.Namespace)
	}

	err = rtc.createClusterRoleRules()
	if err != nil {
		rtc.RollbackE.Data = append(rtc.RollbackE.Data, SA)
		return "", errors.Wrapf(err, "while createClusterRole %s", rtc.User.ClusterRoleName)
	}

	err = rtc.createClusterRole()
	if err != nil {
		rtc.RollbackE.Data = append(rtc.RollbackE.Data, SA, ClusterRole)
		return "", errors.Wrapf(err, "while createClusterRole %s", rtc.User.ClusterRoleName)
	}

	saToken, err := rtc.getServiceAccountToken()
	if err != nil {
		rtc.RollbackE.Data = append(rtc.RollbackE.Data, SA, ClusterRole)
		return "", errors.Wrapf(err, "while getServiceAccountToken from %s", rtc.User.ServiceAccountName)
	}

	err = rtc.createClusterRoleBinding()
	if err != nil {
		rtc.RollbackE.Data = append(rtc.RollbackE.Data, SA, ClusterRole)
		return "", errors.Wrapf(err, "while createClusterRoleBinding %s", rtc.User.ClusterRoleBindingName)
	}
	return saToken, resultE
}

func (rtc *RuntimeClient) createServiceAccount() error {
	saExisted, err := rtc.verifyServiceAccount()
	if err != nil {
		return errors.Wrapf(err, "in verifyServiceAccount")
	}
	if saExisted {
		return nil
	}

	serviceAccount := initServiceAccount(rtc.User)
	_, err = rtc.K8s.CoreV1().ServiceAccounts(rtc.User.Namespace).Create(context.TODO(), serviceAccount, metav1.CreateOptions{})
	return err
}

func (rtc *RuntimeClient) createClusterRoleRules() error {
	if rtc.L2L3OperatiorRole != RUNTIME_ADMIN && rtc.L2L3OperatiorRole != RUNTIME_OPERATOR {
		return fmt.Errorf("role %s not in [%s,%s]", rtc.L2L3OperatiorRole, RUNTIME_ADMIN, RUNTIME_OPERATOR)
	}

	crExist, err := rtc.verifyClusterRoleRules(rtc.L2L3OperatiorRole)
	if err != nil {
		return errors.Wrapf(err, "in verifyClusterRoleRules")
	}
	if crExist {
		return nil
	}

	clusterrole := initClusterRoleRules(rtc.User.ClusterRoleRulesName, rtc.L2L3OperatiorRole, rtc.User.ClusterRoleAggrLabel)
	_, err = rtc.K8s.RbacV1().ClusterRoles().Create(context.TODO(), clusterrole, metav1.CreateOptions{})
	return err
}

func (rtc *RuntimeClient) createClusterRole() error {

	crExist, err := rtc.verifyClusterRole(rtc.L2L3OperatiorRole, rtc.User.ClusterRoleAggrLabel)
	if err != nil {
		return errors.Wrapf(err, "in verifyClusterRoleAggregation")
	}
	if crExist {
		return nil
	}

	clusterrole := initClusterRole(rtc.User.ClusterRoleName, rtc.L2L3OperatiorRole, rtc.User.ClusterRoleAggrLabel)
	_, err = rtc.K8s.RbacV1().ClusterRoles().Create(context.TODO(), clusterrole, metav1.CreateOptions{})
	return err
}

func (rtc *RuntimeClient) createClusterRoleBinding() error {
	objectMeta, roleRef, subjects := initCRBindingE(rtc.User)
	existed, err := rtc.verifyCRBinding(roleRef, subjects)
	if err != nil {
		return errors.Wrapf(err, "in verifyCRBinding")
	}
	if existed {
		return nil
	}
	crbinding := initCRBinding(objectMeta, roleRef, subjects)
	_, err = rtc.K8s.RbacV1().ClusterRoleBindings().Create(context.TODO(), crbinding, metav1.CreateOptions{})
	return err
}

func (rtc *RuntimeClient) deleteServiceAccount() (bool, error) {
	err := rtc.K8s.CoreV1().ServiceAccounts(rtc.User.Namespace).Delete(context.TODO(), rtc.User.ServiceAccountName, metav1.DeleteOptions{})
	if err == nil || apierr.IsNotFound(err) {
		return true, nil
	}
	return false, err
}

func (rtc *RuntimeClient) deleteCRBinding() error {
	err := rtc.K8s.RbacV1().ClusterRoleBindings().Delete(context.TODO(), rtc.User.ClusterRoleBindingName, metav1.DeleteOptions{})
	if err == nil || apierr.IsNotFound(err) {
		return nil
	}
	return err
}

func (rtc *RuntimeClient) deleteClusterRole(name string) (bool, error) {
	err := rtc.K8s.RbacV1().ClusterRoles().Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err == nil || apierr.IsNotFound(err) {
		return true, nil
	}
	return false, err
}

func (rtc *RuntimeClient) verifyServiceAccount() (bool, error) {
	sa, err := rtc.K8s.CoreV1().ServiceAccounts(rtc.User.Namespace).Get(context.TODO(), rtc.User.ServiceAccountName, metav1.GetOptions{})
	if sa != nil && err == nil {
		return true, nil
	}

	if apierr.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (rtc *RuntimeClient) verifyClusterRoleRules(l2L3OperatiorRole string) (bool, error) {
	cr, err := rtc.K8s.RbacV1().ClusterRoles().Get(context.TODO(), rtc.User.ClusterRoleRulesName, metav1.GetOptions{})
	if cr != nil && err == nil {
		if reflect.DeepEqual(cr.Rules, L2L3OperatorPolicyRule[l2L3OperatiorRole]) {
			return true, nil
		} else {
			_, err = rtc.deleteClusterRole(rtc.User.ClusterRoleRulesName)
			if err == nil {
				return false, nil
			} else {
				return false, errors.Wrapf(err, "in deleteClusterRole")
			}
		}
	}

	if apierr.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (rtc *RuntimeClient) verifyClusterRole(l2L3OperatiorRole string, aggregationLabel string) (bool, error) {
	cr, err := rtc.K8s.RbacV1().ClusterRoles().Get(context.TODO(), rtc.User.ClusterRoleName, metav1.GetOptions{})
	if cr != nil && err == nil {
		expectedSelectors := []metav1.LabelSelector{}
		expectedSelectors = append(expectedSelectors, L2L3OperatorAggregationRule[l2L3OperatiorRole]...)
		expectedSelectors = append(expectedSelectors, metav1.LabelSelector{
			MatchLabels: map[string]string{
				aggregationLabel: "true",
			},
		})
		if cr.AggregationRule != nil && reflect.DeepEqual(cr.AggregationRule.ClusterRoleSelectors, expectedSelectors) {
			return true, nil
		} else {
			_, err = rtc.deleteClusterRole(rtc.User.ClusterRoleName)
			if err == nil {
				return false, nil
			} else {
				return false, errors.Wrapf(err, "in deleteClusterRole")
			}
		}
	}

	if apierr.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (rtc *RuntimeClient) verifyCRBinding(roleRef rbacv1.RoleRef, subjects []rbacv1.Subject) (bool, error) {
	crb, err := rtc.K8s.RbacV1().ClusterRoleBindings().Get(context.TODO(), rtc.User.ClusterRoleBindingName, metav1.GetOptions{})
	if crb != nil && err == nil {
		if reflect.DeepEqual(crb.Subjects, subjects) && reflect.DeepEqual(crb.RoleRef, roleRef) {
			return true, nil
		} else {
			err = rtc.deleteCRBinding()
			if err == nil || apierr.IsNotFound(err) {
				return false, nil
			} else {
				return false, errors.Wrapf(err, "in deleteCRBinding")
			}
		}
	}

	if apierr.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (rtc *RuntimeClient) getServiceAccountToken() (string, error) {
	var expirationSeconds int64 = int64(ExpireTime.Seconds())
	tokenRequest := authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expirationSeconds,
		},
	}
	req, err := rtc.K8s.CoreV1().ServiceAccounts(rtc.User.Namespace).CreateToken(context.TODO(), rtc.User.ServiceAccountName, &tokenRequest, metav1.CreateOptions{})

	return req.Status.Token, err
}

func initServiceAccount(user SAInfo) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      user.ServiceAccountName,
			Namespace: user.Namespace,
		},
	}
}

func initClusterRoleRules(clusterRoleName string, l2L3OperatiorRole string, aggregationLabel string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleName,
			Labels: map[string]string{
				aggregationLabel: "true",
			},
		},
		Rules: L2L3OperatorPolicyRule[l2L3OperatiorRole],
	}
}

func initClusterRole(clusterRoleName string, l2L3OperatiorRole string, aggregationLabel string) *rbacv1.ClusterRole {
	clusterrole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleName,
		},
		AggregationRule: &rbacv1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{},
		},
	}
	clusterrole.AggregationRule.ClusterRoleSelectors = append(clusterrole.AggregationRule.ClusterRoleSelectors, L2L3OperatorAggregationRule[l2L3OperatiorRole]...)
	clusterrole.AggregationRule.ClusterRoleSelectors = append(clusterrole.AggregationRule.ClusterRoleSelectors, metav1.LabelSelector{
		MatchLabels: map[string]string{
			aggregationLabel: "true",
		},
	})

	return clusterrole
}

func initCRBindingE(user SAInfo) (metav1.ObjectMeta, rbacv1.RoleRef, []rbacv1.Subject) {
	objectMeta := metav1.ObjectMeta{
		Name: user.ClusterRoleBindingName,
	}

	roleRef := rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     ClusterRole,
		Name:     user.ClusterRoleName,
	}
	subjects := []rbacv1.Subject{
		{
			Kind:      ServiceAccount,
			Name:      user.ServiceAccountName,
			Namespace: user.Namespace,
		},
	}
	return objectMeta, roleRef, subjects
}

func initCRBinding(objectMeta metav1.ObjectMeta, roleRef rbacv1.RoleRef, subjects []rbacv1.Subject) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: objectMeta,
		RoleRef:    roleRef,
		Subjects:   subjects,
	}
}

// Clean service account and cluster role
func (rtc *RuntimeClient) Cleaner() error {
	if len(rtc.RollbackE.Data) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(len(rtc.RollbackE.Data))
	doneCh := make(chan bool)
	errorCh := make(chan error)

	for _, data := range rtc.RollbackE.Data {
		switch data {
		case SA:
			go rtc.RetryDeleteServiceAccount(&wg, errorCh)
		case ClusterRole:
			go rtc.RetryDeleteClusterRoles(&wg, errorCh)
		case ClusterRoleBinding:
			go rtc.RetryDeleteClusterRoleBinding(&wg, errorCh)
		default:
			wg.Done()
		}
	}
	go func() {
		wg.Wait()
		close(errorCh)
		close(doneCh)
	}()

	// process deletion results
	var errWrapped error
	for {
		select {
		case <-doneCh:
			if errWrapped == nil {
				log.Infof("All Kubeconfig Services marked for deletion")
			}
			return errWrapped
		case err := <-errorCh:
			if err != nil {
				if errWrapped == nil {
					errWrapped = err
				} else {
					errWrapped = errors.Wrap(err, errWrapped.Error())
				}
			}
		}
	}
}

func (rtc *RuntimeClient) RetryDeleteServiceAccount(wg *sync.WaitGroup, errorCh chan error) {
	defer wg.Done()

	err := retry.Do(func() error {
		err := rtc.K8s.CoreV1().ServiceAccounts(rtc.User.Namespace).Delete(context.TODO(), rtc.User.ServiceAccountName, metav1.DeleteOptions{})
		if err != nil && !apierr.IsNotFound(err) {
			errorCh <- err
		} else if apierr.IsNotFound(err) {
			return nil
		}

		return errors.Wrapf(err, "Service account \"%s\" still exists in \"%s\" Namespace", rtc.User.ServiceAccountName, rtc.User.Namespace)
	},
		retry.Attempts(20),
		retry.Delay(15*time.Second),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		errorCh <- err
		return
	}
	log.Infof(fmt.Sprintf("SA \"%s\" is removed", rtc.User.ServiceAccountName))
}

func (rtc *RuntimeClient) RetryDeleteClusterRoles(wg *sync.WaitGroup, errorCh chan error) {
	defer wg.Done()
	clusterroles := []string{rtc.User.ClusterRoleName, rtc.User.ClusterRoleRulesName}
	var err error

	err = retry.Do(func() error {
		for _, name := range clusterroles {
			err = rtc.K8s.RbacV1().ClusterRoles().Delete(context.TODO(), name, metav1.DeleteOptions{})

			if err != nil && !apierr.IsNotFound(err) {
				errorCh <- err
			} else if apierr.IsNotFound(err) {
				return nil
			}
		}
		return errors.Wrapf(err, "ClusterRoles \"%v\" still exist", clusterroles)
	},
		retry.Attempts(20),
		retry.Delay(15*time.Second),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		errorCh <- err
		return
	}
	log.Infof(fmt.Sprintf("ClusterRoles \"%v\" are removed", clusterroles))
}

func (rtc *RuntimeClient) RetryDeleteClusterRoleBinding(wg *sync.WaitGroup, errorCh chan error) {
	defer wg.Done()

	err := retry.Do(func() error {
		err := rtc.K8s.RbacV1().ClusterRoleBindings().Delete(context.TODO(), rtc.User.ClusterRoleBindingName, metav1.DeleteOptions{})

		if err != nil && !apierr.IsNotFound(err) {
			errorCh <- err
		} else if apierr.IsNotFound(err) {
			return nil
		}

		return errors.Wrapf(err, "Cluster Role Binding\"%s\" still exists", rtc.User.ClusterRoleName)
	},
		retry.Attempts(20),
		retry.Delay(15*time.Second),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		errorCh <- err
		return
	}
	log.Infof(fmt.Sprintf("Cluster Role Binding \"%s\" is removed", rtc.User.ClusterRoleName))
}
