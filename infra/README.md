# Chronoverse Infrastructure Deployment

This directory contains production deployment options for Chronoverse using different orchestration platforms.

## 🚀 Deployment Options

Choose the deployment method that best fits your infrastructure requirements:

### 🐳 Docker Compose (`compose.prod.yaml`)
**For single-server production deployments**

### ☸️ Kubernetes (`k8s/`)
**For distributed, scalable production deployments**

---

## 🐳 Docker Compose Deployment

### ✅ **Recommended For:**
- **Small to medium-scale deployments** (< 1000 concurrent users)
- **Single-server** or **small cluster** environments
- **Quick production setup** with minimal infrastructure complexity
- **Cost-effective** deployments on VPS or bare metal
- **Development** and **testing** environments
- **Proof of concept** deployments
- **Teams without Kubernetes expertise**
- **Simple monitoring** and troubleshooting requirements

### ❌ **Not Recommended For:**
- **High-scale production** (> 1000 concurrent users)
- **Multi-datacenter** deployments
- **Complex networking** requirements
- **Auto-scaling** needs
- **Zero-downtime deployments**
- **Advanced security** requirements (RBAC, network policies)
- **Multi-tenancy** deployments
- **Complex compliance** requirements

### 📋 **Docker Compose Features:**
- ✅ **Production-ready** configuration
- ✅ **Multi-replica workers** for performance
- ✅ **Health checks** and restart policies
- ✅ **Resource limits** and constraints
- ✅ **TLS encryption** between services
- ✅ **Persistent storage** with named volumes
- ✅ **Observability** with LGTM stack
- ✅ **Load balancing** with Nginx

### 🚀 **Quick Start:**
```bash
cd infra/
docker compose -f compose.prod.yaml up -d
```

### 📊 **Resource Requirements:**
- **Minimum**: 4 CPU cores, 8GB RAM, 50GB storage
- **Recommended**: 8 CPU cores, 16GB RAM, 100GB storage
- **Network**: Single server with Docker support

---

## ☸️ Kubernetes Deployment

### ✅ **Recommended For:**
- **Large-scale production** (> 1000 concurrent users)
- **Enterprise environments** with existing Kubernetes infrastructure
- **Multi-datacenter** and **cloud-native** deployments
- **Auto-scaling** and **high availability** requirements
- **Zero-downtime** deployments and rolling updates
- **Advanced security** needs (RBAC, network policies, pod security)
- **Multi-tenancy** and **namespace isolation**
- **Complex compliance** and audit requirements
- **DevOps teams** with Kubernetes expertise
- **Cloud providers** (AWS EKS, GCP GKE, Azure AKS)

### ❌ **Not Recommended For:**
- **Small deployments** (< 100 concurrent users)
- **Single-server** environments
- **Quick prototyping** or testing
- **Teams without Kubernetes knowledge**
- **Budget-constrained** deployments
- **Simple infrastructure** requirements

### 📋 **Kubernetes Features:**
- ✅ **Horizontal pod autoscaling** (HPA)
- ✅ **Zero-downtime** rolling updates
- ✅ **Advanced security** (RBAC, SecurityContexts, NetworkPolicies)
- ✅ **Pod disruption budgets** for high availability
- ✅ **Persistent volumes** with storage classes
- ✅ **Service mesh** ready (Istio, Linkerd)
- ✅ **Multi-environment** support (dev, staging, prod)
- ✅ **Resource quotas** and limits
- ✅ **Advanced monitoring** and observability
- ✅ **Ingress controllers** with SSL termination
- ✅ **Secrets management** and encryption

### 🚀 **Quick Start:**
```bash
cd infra/k8s/

# Local development
./deploy.sh --local

# Cloud production
./deploy.sh
```

### 📊 **Resource Requirements:**
- **Minimum**: 3 nodes, 2 CPU cores each, 4GB RAM each
- **Recommended**: 5+ nodes, 4+ CPU cores each, 8GB+ RAM each
- **Network**: Kubernetes cluster with LoadBalancer support

---

## 🤔 **Decision Matrix**

| Factor | Docker Compose | Kubernetes |
|--------|----------------|------------|
| **Setup Complexity** | 🟢 Simple | 🟡 Complex |
| **Learning Curve** | 🟢 Low | 🔴 High |
| **Scalability** | 🟡 Limited | 🟢 Excellent |
| **High Availability** | 🟡 Basic | 🟢 Advanced |
| **Resource Usage** | 🟢 Lower | 🟡 Higher |
| **Monitoring** | 🟡 Basic | 🟢 Advanced |
| **Security** | 🟡 Good | 🟢 Enterprise |
| **Updates** | 🟡 Manual | 🟢 Automated |
| **Multi-env Support** | 🟡 Limited | 🟢 Excellent |
| **Cloud Integration** | 🟡 Basic | 🟢 Native |

## 📈 **Deployment Sizing Guide**

### 🐳 **Docker Compose Scenarios:**

#### **Small Production** (< 100 users)
```yaml
# Single server: 2 CPU, 4GB RAM
- 1x Database
- 1x Each service 
- 1x Each worker
```

#### **Medium Production** (100-1000 users)
```yaml
# Single server: 4-8 CPU, 8-16GB RAM
- 1x Database (scaled)
- 1x Each service
- 2x Critical workers
```

### ☸️ **Kubernetes Scenarios:**

#### **Enterprise Production** (1000+ users)
```yaml
# Multi-node cluster
- 3x Database replicas (HA)
- 3x Each service (load balanced)
- 5x Critical workers (auto-scaled)
- Multi-zone deployment
```

#### **Cloud Production** (Variable load)
```yaml
# Auto-scaling cluster  
- StatefulSet databases
- HPA for services (2-10 replicas)
- HPA for workers (2-20 replicas)
- Ingress with SSL termination
```

---

## 📂 **Directory Structure**

```
infra/
├── README.md                    # This file
├── compose.prod.yaml            # Docker Compose production config
└── k8s/                         # Kubernetes deployment manifests
    ├── README.md               # Kubernetes-specific documentation
    ├── deploy.sh              # Automated deployment script
    ├── namespace.yaml         # Chronoverse namespace
    ├── secrets/               # Application secrets
    ├── configmaps/           # Configuration files
    ├── security/             # RBAC and security policies
    ├── databases/            # Database StatefulSets
    ├── deployments/          # Application services
    ├── ingress/              # Load balancer and routing
    ├── networking/           # MetalLB configuration
    ├── storage/              # Persistent volumes
    └── jobs/                 # Init jobs and migrations
```

---

## 🔧 **Migration Path**

### **Docker Compose → Kubernetes**
1. **Start with Docker Compose** for MVP/initial deployment
2. **Learn Kubernetes** while running on Docker Compose
3. **Set up Kubernetes cluster** (cloud or on-premise)
4. **Migrate data** using backup/restore procedures
5. **Deploy on Kubernetes** using the provided manifests
6. **Switch traffic** with DNS updates

### **Key Migration Considerations:**
- **Data persistence**: Plan database migration strategy
- **Configuration**: Review environment variables and secrets
- **Networking**: Update DNS and load balancer configurations
- **Monitoring**: Adapt observability stack to Kubernetes
- **Security**: Implement Kubernetes-specific security policies

---

## 🆘 **Support & Troubleshooting**

### **Docker Compose Issues:**
```bash
# Check service status
docker compose -f compose.prod.yaml ps

# View logs
docker compose -f compose.prod.yaml logs service-name

# Restart services
docker compose -f compose.prod.yaml restart
```

### **Kubernetes Issues:**
```bash
# Check pod status
kubectl get pods -n chronoverse

# View logs
kubectl logs -f deployment/service-name -n chronoverse

# Debug networking
kubectl get services,ingress -n chronoverse
```

---

## 📞 **Quick Commands**

### **Docker Compose:**
```bash
# Production deployment
docker compose -f compose.prod.yaml up -d

# Scale workers
docker compose -f compose.prod.yaml up -d --scale workflow-worker=3

# Update services
docker compose -f compose.prod.yaml pull && docker compose -f compose.prod.yaml up -d

# Cleanup
docker compose -f compose.prod.yaml down -v
```

### **Kubernetes:**
```bash
# Local deployment
cd k8s && ./deploy.sh --local

# Cloud deployment  
cd k8s && ./deploy.sh

# Check status
kubectl get all -n chronoverse

# Scale services
kubectl scale deployment/users-service --replicas=3 -n chronoverse
```

---

**Choose the deployment method that aligns with your infrastructure maturity, team expertise, and scaling requirements.**