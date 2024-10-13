## TopFull Open Source Implementation

### Overview

This repository contains an open-source implementation of **TopFull**, an adaptive overload control system for SLO-oriented microservices, based on the algorithm proposed in the paper *"TopFull: An Adaptive Top-Down Overload Control for SLO-Oriented Microservices"*.
TopFull is designed to mitigate overloads in microservices by performing API-wise load control. The system dynamically adjusts the admitted rates of APIs at entry points based on the severity of the overload, using a Reinforcement Learning (RL)-based rate controller.

### Thread Safety

The implementation uses **atomic operations** and **sync.Map** to manage shared states such as token buckets, SLO metrics, and goodput counters, ensuring thread safety and high performance in concurrent environments. This is crucial for microservices where numerous requests need to be handled simultaneously.

### Colocated Python Program Requirement

The core RL training and inference are **not part of this Go repository** and are handled by a **separate Python program** that must run alongside this Go-based control system. This Python program manages the learning agent, which is responsible for adjusting the rate limiting policies based on the real-time performance metrics collected by the Go controller.

For training or inference to function properly:
- The Python RL agent must be running concurrently with this controller.
- Communication between the Go controller and the Python RL agent is required to fetch the rate limiting decisions based on learned policies.

This separation ensures that the rate limiting control package remains lightweight and efficient while leveraging the sophisticated RL logic handled externally.

#### Python RL Agent Example
```python
import requests, time
import numpy as np
import gymnasium as gym
from gymnasium import spaces
from stable_baselines3 import PPO
import os, subprocess, multiprocessing
import sys

from slo import get_slo, get_sustainable_load


def get_server_address(entry_point, port=8082):
    try:
        # Get the Cluster IP of the service
        service_ip = subprocess.check_output(
            f"kubectl get service {entry_point} -o=jsonpath='{{.spec.clusterIP}}'", shell=True
        ).decode('utf-8').strip()

        # Get the NodePort (optional, depending on your use case)
        node_port = subprocess.check_output(
            f"kubectl get service {entry_point} -o=jsonpath='{{.spec.ports[0].nodePort}}'", shell=True
        ).decode('utf-8').strip()

        # Construct the service URL
        server_address = f"http://{service_ip}:{port}"
        print(f"[DEBUG] Server address: {server_address}")
        return server_address
    except subprocess.CalledProcessError as e:
        print(f"Error retrieving server address: {e}")
        return None
    

class RealAppEnv(gym.Env):
    def __init__(self, app_name, apis, max_steps=50, penalty_coefficient=1.0, entry_point="nginx"):
        super(RealAppEnv, self).__init__()
        self.app_name = app_name
        self.max_steps = max_steps
        self.penalty_coefficient = penalty_coefficient  # Penalty coefficient (ρ)

        self.apis = apis  # List of APIs in the cluster

        # Priority map for APIs
        self.priority_map = {
            "Some": 1,
            "APIs": 2,
            "ForExample": 3
        }

        # Get the server address from the Kubernetes service
        server_address = get_server_address(entry_point)
        if server_address is None:
            raise ValueError("Error retrieving server address")
        
        self.server_address = server_address
        print(f"[DEBUG] Server address initialized: {self.server_address}")

        # updated for multiple APIs
        self.rate_limits = {api: 1000 for api in self.apis}
        self.prev_total_goodput = None  # To store previous goodput for ΔGoodput calculation
        self.current_latencies = {api: 0 for api in self.apis}
        self.current_step = 0

        # Observation space: [total_latency, total_goodput]
        self.observation_space = spaces.Box(low=0, high=np.inf, shape=(2,), dtype=np.float32)

        # Action space: single continuous action to adjust the rate limit
        self.action_space = spaces.Box(low=-0.5, high=0.5, shape=(1,), dtype=np.float32)

    def reset(self, seed=None, options=None):
        super().reset(seed=seed)
        self.current_step = 0
        self.rate_limits = {api: 1000 for api in self.apis}  # Reset rate limit
        # You can set a random seed here for the environment
        print(f"[DEBUG] Environment reset: {self.rate_limits}")
        if seed is not None:
            np.random.seed(seed)
        return self._get_observation(), {}

    def _get_observation(self):
        
        aggregate_goodput = 0
        aggregate_rate_limit = 0
        max_latency = 0
        
        for api in self.apis:
            params = {"method": api}
            # GET metrics from the Go server
            response = requests.get(f"{self.server_address}/metrics", params=params)
            metrics = response.json()
            total_latency = metrics["latency"]
            total_goodput = metrics["goodput"]
            # Calculate the ratio of goodput to the current rate limit
            print(f"[DEBUG] Metrics for {api}: Goodput={total_goodput}, Latency={total_latency}")

            aggregate_goodput += total_goodput
            aggregate_rate_limit += self.rate_limits[api]
            max_latency = max(max_latency, total_latency)

        goodput_ratio = aggregate_goodput / aggregate_rate_limit if aggregate_rate_limit > 0 else 0
        
        # The observation includes:
        # 1. Ratio of goodput to the current rate limit
        # 2. The maximum latency across candidate APIs (already max_latency)
        print(f"[DEBUG] Observation: Goodput Ratio={goodput_ratio}, Max Latency={max_latency}")
        return np.array([goodput_ratio, max_latency], dtype=np.float32)

    def step(self, action):
        start_time = time.time()  # Record start time

        # Apply Algorithm 1 from the paper
        action_rl = action[0]
        print(f"[DEBUG] Action received: {action_rl*100}% rate adjustment")

        if action_rl > 0:
            # Highest priority API (lowest priority value)
            sorted_apis = sorted(self.apis, key=lambda api: self.priority_map.get(api, float('inf')))
        else:
            # Lowest priority API (highest priority value)
            sorted_apis = sorted(self.apis, key=lambda api: -self.priority_map.get(api, float('inf')))
        
        print(f"[DEBUG] Sorted APIs: {sorted_apis} in the order of target for the action")

        # Loop through the sorted APIs and apply the action to the first valid one
        for api in sorted_apis:
            max_sustainable_load = get_sustainable_load(api)

            # Example bounds for rate limit adjustment
            upper_bound = 1.5 * max_sustainable_load
            lower_bound = 100

            current_rate_limit = self.rate_limits[api]

            # If we're increasing the rate and the rate limit is below the upper bound, apply the action
            if action_rl > 0:
                if current_rate_limit < upper_bound:
                    self.rate_limits[api] = min(upper_bound, current_rate_limit * (1 + action_rl))
                    print(f"[DEBUG] Increasing rate for {api}: {self.rate_limits[api]}")
                    break  # Apply the action to the first valid API and exit the loop
                else:
                    print(f"[DEBUG] Rate limit for {api} is {current_rate_limit} and already at the upper bound {upper_bound}")

            # If we're decreasing the rate and the rate limit is above the lower bound, apply the action
            if action_rl < 0:
                if current_rate_limit > lower_bound:
                    self.rate_limits[api] = max(lower_bound, current_rate_limit * (1 + action_rl))
                    print(f"[DEBUG] Decreasing rate for {api}: {self.rate_limits[api]}")
                    break  # Apply the action to the first valid API and exit the loop
                else:
                    print(f"[DEBUG] Rate limit for {api} is {current_rate_limit} and already at the lower bound {lower_bound}")

        
        # SET the new rate limit on the Go server for the selected API
        params = {'method': api}
        data = {'rate_limit': self.rate_limits[api]}
        requests.post(f"{self.server_address}/set_rate", params=params, json=data)
        
        observation = self._get_observation()

        # Extract goodput_ratio and max_latency from the observation
        goodput_ratio, max_latency = observation

        # For total_goodput, compute it based on the goodput_ratio and current total rate limit
        total_goodput = goodput_ratio * sum(self.rate_limits.values())

        # Calculate the change in goodput (ΔGoodput)
        delta_goodput = total_goodput - self.prev_total_goodput if self.prev_total_goodput is not None else 0

        # Update previous goodput
        self.prev_total_goodput = total_goodput

        # Calculate the penalty for latency exceeding the SLO
        latency_penalty = max(0, max_latency - max(get_slo(api) for api in self.apis))

        # Reward calculation
        reward = delta_goodput - self.penalty_coefficient * latency_penalty

        self.current_step += 1
        done = self.current_step >= self.max_steps

        elapsed_time = time.time() - start_time
        time.sleep(max(0, 1 - elapsed_time))  # Enforce 1-second interval
        
        print(f"[DEBUG] Reward: {reward}, Goodput: {total_goodput}, Latency Penalty: {latency_penalty}")
        return observation, reward, done, False, {}  # Return False for 'truncated' as this example doesn't use truncation.

    def close(self):
        pass


def run_rl_model_for_cluster(cluster_name, apis, entry_point, methods):
    # Set entry_point and app_name based on the method
    if methods == "all-social":
        app_name = "Social-Network"  # For example, a social network application
    else:
        app_name = methods  # Default case uses the method as app_name

    print(f"[DEBUG] Applying model on the {app_name} application")           
    # Pass the 'apis' argument to the RealAppEnv class
    env = RealAppEnv(app_name=app_name, apis=apis, entry_point=entry_point, penalty_coefficient=0.01)
    print(f"[DEBUG] Environment initialized: {env}")

    # Load the final trained model
    final_model_path = f"{app_name}_checkpoints/{app_name}_final_model.zip"
    model = PPO.load(final_model_path, env=env)
    print(f"[DEBUG] Model loaded: {final_model_path}")

    # Apply the trained model in the real environment
    obs, _ = env.reset()
    for i in range(1000):
        action, _ = model.predict(obs, deterministic=True)
        obs, reward, done, _, _ = env.step(action)
        if done:
            obs, _ = env.reset()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("Usage: python rl-topfull.py <method> <entry_point>")
        sys.exit(1)

    methods = sys.argv[1]
    entry_point = sys.argv[2]
    print(f"Applying model on the {methods} application")

    # Define clusters and APIs
    if methods == "all-social":
        clusters = {
            "cluster1": ["compose", "home-timeline"],
            "cluster2": ["user-timeline"]
        }
    else:
        clusters = {
            "default": [methods]
        }
    
    # Test the connection and 2 HTTP requests first
    try:
        for cluster_name, apis in clusters.items():
            for api in apis:
                print(f"[DEBUG] Testing connection to {api}")
                server_url = get_server_address(entry_point)
                response = requests.get(f"{server_url}/metrics", params={"method": api})
                if response.status_code == 200:
                    print(f"[DEBUG] Connection to {api} successful")
                else:
                    print(f"[ERROR] Failed to connect to {api}")
    except Exception as e:
        print(f"[ERROR] {e}")

    # Create a list to hold the processes
    processes = []

    # Loop through each cluster and apply the RL model
    for cluster_name, apis in clusters.items():
        # Create a new process for each cluster
        p = multiprocessing.Process(target=run_rl_model_for_cluster, args=(cluster_name, apis, entry_point, methods))
        processes.append(p)
        p.start()

    # Wait for all processes to finish
    for p in processes:
        p.join()
```

This Python component provides the RL agent for TopFull, a system designed for adaptive rate-limiting in microservices. The code interacts with a Go-based controller and adjusts API rate limits based on real-time metrics like goodput and latency. In the example above, the Python environment fetches metrics from the Go server and uses the PPO algorithm and trained models to infer the optimal rate-limiting policy. The Go controller handles overload control, while this Python part manages RL agent to dynamically adjust the rate limits based on system conditions.