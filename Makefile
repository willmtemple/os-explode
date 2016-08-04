build:
	go build
	docker build -t exploder .

install:
	oc create serviceaccount exploder
	oc create -f templates/exploder-openshift.yaml
	oadm policy add-scc-to-user privileged system:serviceaccount:default:exploder
	oadm policy add-cluster-role-to-user cluster-admin system:serviceaccount:default:exploder

#test:
