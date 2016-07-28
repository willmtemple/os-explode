build:
        # TODO: add golang build here
	docker build -t exploder .

install:
	oc create serviceaccount exploder
	oc new-app -f templates/exploder-openshift.yaml
	oadm policy add-scc-to-user privileged system:serviceaccount:default:exploder

#test:
