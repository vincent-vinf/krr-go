- ### 推荐原理

  依赖指标：

  - `container_cpu_usage_seconds_total` 容器 CPU
  - `container_memory_rss` 容器 内存
  - `kube_replicaset_owner` replicaSet与deployment关系
  - `kube_pod_owner` pod和workload的关系
  - `kube_pod_status_phase` pod状态
  - `kube_pod_container_info` pod容器信息
  - `kube_pod_container_resource_limits` 容器limit值
  - `kube_pod_container_resource_requests` 容器request值

  实现步骤

  1. 通过kube_replicaset_owner和kube_pod_owner获取workload以及对应pod
  2. 通过kube_pod_container_info获取pod对应的容器
  3. 通过kube_pod_container_resource_limits和kube_pod_container_resource_requests获取用户设置的资源配额
  4. 获取容器container_cpu_usage_seconds_total和container_memory_rss的指标
     * 内存
       * request：过去7天，每15分钟统计内存使用量，取最大值
       * limit：过去7天，每15分钟统计内存使用量，取最大值，并添加15%的缓冲
     * CPU
       * request：过去7天，每15分钟统计cpu使用量，取85百分位点
       * limit：过去7天，每15分钟统计cpu使用量，取95百分位点



todo

* 指标可配

  