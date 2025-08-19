#!/bin/bash

set -e

# Function to detect appropriate IP range for MetalLB
detect_metallb_ip_range() {
  echo "🔍 Detecting appropriate IP range for MetalLB..."
  
  # Get node internal IP to determine network
  NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
  echo "📍 Node IP detected: $NODE_IP"
  
  # Extract network prefix (e.g., 192.168.1.100 -> 192.168.1)
  NETWORK_PREFIX=$(echo $NODE_IP | cut -d. -f1-3)
  
  # Generate safe IP range avoiding node IP
  NODE_LAST_OCTET=$(echo $NODE_IP | cut -d. -f4)
  
  # Choose range based on node IP to avoid conflicts
  if [ "$NODE_LAST_OCTET" -lt 100 ]; then
    # Node is in lower range, use higher range
    IP_START="$NETWORK_PREFIX.200"
    IP_END="$NETWORK_PREFIX.250"
  else
    # Node is in higher range, use lower range  
    IP_START="$NETWORK_PREFIX.50"
    IP_END="$NETWORK_PREFIX.99"
  fi
  
  echo "🎯 Generated IP range: $IP_START-$IP_END"
  echo "✅ This range avoids conflicts with node IP $NODE_IP"
}

# Function to create dynamic MetalLB configuration
create_metallb_config() {
  local ip_range="$1"
  
  cat > /tmp/metallb-config-dynamic.yaml << EOF
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: chronoverse-pool
  namespace: metallb-system
spec:
  addresses:
  - $ip_range
  autoAssign: true
  avoidBuggyIPs: false
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: chronoverse-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - chronoverse-pool
EOF
  
  echo "📝 Created dynamic MetalLB configuration at /tmp/metallb-config-dynamic.yaml"
}

# Parse command line arguments
LOCAL_DEPLOYMENT=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --local)
      LOCAL_DEPLOYMENT=true
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --local    Deploy for local development (Docker Desktop/kind) with port-forwarding"
      echo "  --help     Show this help message"
      exit 0
      ;;
    *)
      echo "Unknown option $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

if [ "$LOCAL_DEPLOYMENT" = true ]; then
  echo "🚀 Deploying Chronoverse to Kubernetes (LOCAL MODE)..."
  echo "📍 Detected local deployment - will use port-forwarding for access"
else
  echo "🚀 Deploying Chronoverse to Kubernetes (CLOUD MODE)..."
  echo "☁️  Detected cloud deployment - will use LoadBalancer for access"
fi

# Apply namespace first
echo "📦 Creating namespace..."
kubectl apply -f namespace.yaml

# Apply persistent storage
echo "💾 Creating persistent storage..."
kubectl apply -f storage/hostpath-volumes.yaml

# Apply RBAC and security
echo "🛡️ Creating RBAC and security policies..."
kubectl apply -f security/

# Apply secrets first
echo "🔐 Creating secrets..."
kubectl apply -f secrets/

# Apply configuration
echo "⚙️ Creating configuration..."
kubectl apply -f configmaps/

# Step 1: Certificate generation and wait conditions
echo "🔐 Running certificate initialization..."
kubectl apply -f deployments/init-jobs.yaml

echo "⏳ Waiting for certificate generation to complete..."
kubectl wait --for=condition=complete job/init-certs -n chronoverse --timeout=300s
kubectl wait --for=condition=complete job/init-service-certs -n chronoverse --timeout=300s

# Step 2: Databases setup and wait conditions
echo "🗄️ Deploying databases..."
kubectl apply -f databases/

echo "⏳ Waiting for databases to be ready..."
kubectl wait --for=condition=ready pod -l app=postgres -n chronoverse --timeout=300s
kubectl wait --for=condition=ready pod -l app=clickhouse -n chronoverse --timeout=300s
kubectl wait --for=condition=ready pod -l app=redis -n chronoverse --timeout=300s
kubectl wait --for=condition=ready pod -l app=kafka -n chronoverse --timeout=300s

# Step 3: LGTM and docker-proxy supporting services
echo "🔧 Deploying supporting services..."
kubectl apply -f deployments/docker-proxy.yaml
kubectl wait --for=condition=ready pod -l app=lgtm -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/docker-proxy -n chronoverse --timeout=300s

# Step 4: Database migrations and wait conditions
echo "🔄 Running database migrations..."
kubectl apply -f jobs/database-migration.yaml

echo "⏳ Waiting for database migration to complete..."
kubectl wait --for=condition=complete job/database-migration -n chronoverse --timeout=300s

# Step 5: Application services and wait conditions
echo "🎯 Deploying application services..."
kubectl apply -f deployments/users-service.yaml
kubectl apply -f deployments/workflows-service.yaml
kubectl apply -f deployments/jobs-service.yaml
kubectl apply -f deployments/notifications-service.yaml
kubectl apply -f deployments/analytics-service.yaml

echo "⏳ Waiting for application services to be ready..."
kubectl wait --for=condition=available deployment/users-service -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/workflows-service -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/jobs-service -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/notifications-service -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/analytics-service -n chronoverse --timeout=300s

# Step 6: Workers and wait conditions
echo "⚡ Deploying workers..."
kubectl apply -f deployments/workers.yaml

echo "⏳ Waiting for workers to be ready..."
kubectl wait --for=condition=available deployment/scheduling-worker -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/workflow-worker -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/execution-worker -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/joblogs-processor -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/analytics-processor -n chronoverse --timeout=300s

# Step 7: Server and dashboard
echo "🌐 Deploying server and dashboard..."
kubectl apply -f deployments/server.yaml
kubectl apply -f deployments/dashboard.yaml

echo "⏳ Waiting for server and dashboard..."
kubectl wait --for=condition=available deployment/server -n chronoverse --timeout=300s
kubectl wait --for=condition=available deployment/dashboard -n chronoverse --timeout=300s

# Step 8: Remaining checks and nginx
echo "🔀 Deploying nginx load balancer..."
kubectl apply -f ingress/

echo "⏳ Waiting for nginx..."
kubectl wait --for=condition=available deployment/nginx -n chronoverse --timeout=300s

# Step 9: Deploy networking configuration based on environment
if [ "$LOCAL_DEPLOYMENT" = false ]; then
  echo "🌐 Deploying MetalLB for LoadBalancer support..."
  
  # Check if MetalLB is already installed
  if ! kubectl get namespace metallb-system >/dev/null 2>&1; then
    echo "📦 Installing MetalLB..."
    kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.8/config/manifests/metallb-native.yaml
    
    echo "⏳ Waiting for MetalLB to be ready..."
    kubectl wait --namespace metallb-system \
                 --for=condition=ready pod \
                 --selector=app=metallb \
                 --timeout=90s
  else
    echo "✅ MetalLB already installed"
  fi
  
  # Apply MetalLB configuration
  echo "⚙️ Configuring MetalLB IP address pool..."
  
  # Check if user provided a custom cloud config
  if [ -f "networking/metallb-config-cloud.yaml" ] && grep -q "192.168.1.100-192.168.1.110" networking/metallb-config-cloud.yaml; then
    echo "⚠️  Detected default IP range in metallb-config-cloud.yaml"
    echo "🔄 Generating dynamic configuration to avoid conflicts..."
    
    # Detect appropriate IP range and create dynamic config
    detect_metallb_ip_range
    create_metallb_config "$IP_START-$IP_END"
    kubectl apply -f /tmp/metallb-config-dynamic.yaml
    
  elif [ -f "networking/metallb-config-cloud.yaml" ]; then
    echo "🌍 Using custom cloud MetalLB configuration"
    kubectl apply -f networking/metallb-config-cloud.yaml
    
  else
    echo "🔄 Generating dynamic MetalLB configuration..."
    
    # Detect appropriate IP range and create dynamic config
    detect_metallb_ip_range
    create_metallb_config "$IP_START-$IP_END"
    kubectl apply -f /tmp/metallb-config-dynamic.yaml
  fi
  
  echo "⏳ Waiting for ingress controller external IP..."
  kubectl wait --for=jsonpath='{.status.loadBalancer.ingress}' service/ingress-nginx-controller -n ingress-nginx --timeout=300s
  
  # Show assigned external IP
  EXTERNAL_IP=$(kubectl get service ingress-nginx-controller -n ingress-nginx -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
  if [ -n "$EXTERNAL_IP" ]; then
    echo "🎉 External IP assigned: $EXTERNAL_IP"
  fi
  
  # Cleanup temporary file
  rm -f /tmp/metallb-config-dynamic.yaml
else
  echo "🏠 Skipping MetalLB deployment for local environment"
  echo "📝 Local deployments will use port-forwarding for access"
fi

# Final health check
echo "🔍 Running final health checks..."
kubectl get pods -n chronoverse
echo ""

# Check if all pods are ready
READY_PODS=$(kubectl get pods -n chronoverse --no-headers | grep -c " 1/1 \|2/2 \|3/3 ")
TOTAL_PODS=$(kubectl get pods -n chronoverse --no-headers | wc -l)

if [ "$READY_PODS" -eq "$TOTAL_PODS" ]; then
    echo "✅ All pods are ready!"
else
    echo "⚠️  Warning: $((TOTAL_PODS - READY_PODS)) pods are not ready yet"
    echo "   You may need to wait a bit longer or check logs"
fi

echo ""
echo "✅ Deployment complete!"
echo ""
echo "🎉 Chronoverse is now running on Kubernetes!"
echo ""

# Set up access based on deployment type
if [ "$LOCAL_DEPLOYMENT" = true ]; then
  echo "🔗 Setting up port-forwarding for local access..."
  
  # Kill any existing port-forward processes
  pkill -f "kubectl port-forward.*ingress-nginx-controller" >/dev/null 2>&1 || true
  
  # Start port-forwarding in background
  nohup kubectl port-forward -n ingress-nginx service/ingress-nginx-controller 8080:80 >/dev/null 2>&1 &
  PORT_FORWARD_PID=$!
  
  # Wait a moment for port-forward to establish
  sleep 2
  
  # Check if port-forward is working
  if kill -0 $PORT_FORWARD_PID 2>/dev/null; then
    echo "✅ Port-forwarding established on localhost:8080"
    echo "📝 Port-forward PID: $PORT_FORWARD_PID"
  else
    echo "⚠️  Port-forwarding failed to start"
  fi
fi

if [ "$LOCAL_DEPLOYMENT" = true ]; then
  echo "🏠 LOCAL DEPLOYMENT ACCESS:"
  echo "────────────────────────────────"
  echo "🌐 Access Chronoverse Dashboard:"
  echo "   → http://localhost:8080"
  echo ""
  echo "📊 Access Grafana (LGTM):"
  echo "   → kubectl port-forward svc/lgtm 3000:3000 -n chronoverse"
  echo "   → Then visit: http://localhost:3000"
  echo ""
  echo "🔗 Manage port-forwarding:"
  echo "   → Stop: pkill -f 'kubectl port-forward.*ingress-nginx-controller'"
  echo "   → Restart: kubectl port-forward -n ingress-nginx service/ingress-nginx-controller 8080:80"
  echo ""
else
  echo "☁️  CLOUD DEPLOYMENT ACCESS:"
  echo "──────────────────────────────"
  
  # Get current external IP
  CURRENT_EXTERNAL_IP=$(kubectl get service ingress-nginx-controller -n ingress-nginx -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
  
  if [ -n "$CURRENT_EXTERNAL_IP" ]; then
    echo "🌍 Access Chronoverse Dashboard:"
    echo "   → http://$CURRENT_EXTERNAL_IP"
    echo ""
    echo "📋 Your LoadBalancer IP: $CURRENT_EXTERNAL_IP"
  else
    echo "🌐 Get LoadBalancer external IP:"
    echo "   → kubectl get service ingress-nginx-controller -n ingress-nginx"
    echo ""
    echo "🌍 Access Chronoverse Dashboard:"
    echo "   → http://<EXTERNAL-IP>"
    echo "   → (Use the EXTERNAL-IP from the command above)"
  fi
  echo ""
  echo "📊 Access Grafana (LGTM):"
  echo "   → kubectl port-forward svc/lgtm 3000:3000 -n chronoverse"
  echo "   → Then visit: http://localhost:3000"
  echo ""
  echo "🏷️  For custom domains:"
  echo "   → Point your domain DNS to the EXTERNAL-IP"
  echo "   → Update ingress host rules as needed"
  echo ""
fi

echo "🔧 MONITORING & TROUBLESHOOTING:"
echo "──────────────────────────────────"
echo "📋 Monitor deployment:"
echo "   → kubectl get pods -n chronoverse"
echo "   → kubectl logs -f deployment/server -n chronoverse"
echo ""
echo "🐛 Troubleshooting:"
echo "   → kubectl describe pods -n chronoverse"
echo "   → kubectl get events -n chronoverse --sort-by='.lastTimestamp'"
echo "   → kubectl get service ingress-nginx-controller -n ingress-nginx"