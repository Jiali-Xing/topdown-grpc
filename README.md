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

```python
import requests
import gymnasium as gym
from stable_baselines3 import PPO

class RealAppEnv(gym.Env):
    def __init__(self, apis, server_address, penalty_coefficient=1.0):
        super(RealAppEnv, self).__init__()
        self.apis = apis  # List of APIs
        self.server_address = server_address  # Address of the Go server
        self.penalty_coefficient = penalty_coefficient

        # Observation space: goodput ratio and max latency
        self.observation_space = gym.spaces.Box(low=0, high=float('inf'), shape=(2,), dtype=float)
        # Action space: continuous adjustment of rate limit
        self.action_space = gym.spaces.Box(low=-0.5, high=0.5, shape=(1,), dtype=float)

    def reset(self):
        # Reset rate limits and observations
        self.rate_limits = {api: 1000 for api in self.apis}
        return self._get_observation()

    def step(self, action):
        # Process action and update rate limits
        for api in self.apis:
            # Apply action logic here
            pass
        observation = self._get_observation()
        reward = self._calculate_reward()
        done = False  # Modify based on logic
        return observation, reward, done, {}

    def _get_observation(self):
        # Fetch metrics from the Go server
        response = requests.get(f"{self.server_address}/metrics")
        metrics = response.json()
        # Process the metrics (e.g., goodput and latency)
        return [metrics["goodput_ratio"], metrics["max_latency"]]

    def _calculate_reward(self):
        # Implement reward calculation logic
        return 0

if __name__ == "__main__":
    # Example usage with stable-baselines3 PPO
    server_address = "http://localhost:8082"
    apis = ["api1", "api2"]
    env = RealAppEnv(apis=apis, server_address=server_address)

    model = PPO("MlpPolicy", env, verbose=1)
    model.learn(total_timesteps=10000)

    obs = env.reset()
    for _ in range(100):
        action, _ = model.predict(obs, deterministic=True)
        obs, reward, done, _ = env.step(action)
        if done:
            obs = env.reset()
```

This Python component provides the RL agent for TopFull, a system designed for adaptive rate-limiting in microservices. The code interacts with a Go-based controller and adjusts API rate limits based on real-time metrics like goodput and latency.

In the example above, the Python environment fetches metrics from the Go server and uses the PPO algorithm from stable-baselines3 to learn the optimal rate-limiting policy.

The Go controller handles overload control, while this Python part manages RL training and inference to dynamically adjust the rate limits based on system conditions.