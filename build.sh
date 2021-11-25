#!/bib/bash


work_directory=`pwd`

export GOROOT="/home/work/soft/go_1.16/"
export PATH=$GOROOT/bin:$PATH

export GOPATH="${work_directory}/go/tmp_compile_kubernetes/"

rm -rf $GOPATH


mkdir -pv ${GOPATH}
#if [ $? -ne 0 ];then
#    echo "Failed to mkdir ${GOPATH}"
#fi

#GOPATH="/home/work/go"
rm -rf ${work_directory}/output/bin && mkdir -pv ${work_directory}/output/bin
rm -rf ${GOPATH}/src/k8s.io/kubernetes/
mkdir -pv ${GOPATH}/src/k8s.io/kubernetes/
cp * ${GOPATH}/src/k8s.io/kubernetes/ -r

git config --global http.sslVerify false
go env -w GO111MODULE="on"
go env -w GOPROXY=http://goproxy.duxiaoman-int.com/nexus/repository/go-public/
go env -w GOPRIVATE="*.duxiaoman-int.com"
go env -w GONOPROXY="**.duxiaoman-int.com**"
go env -w GONOSUMDB="*"
go env -w GOSUMDB=off

pushd ${GOPATH}/src/k8s.io/kubernetes && make all && (mv _output/bin/* ${work_directory}/output/bin && popd) || (echo "Failed to build k8s " && exit 1)

echo "Success to build k8s"
