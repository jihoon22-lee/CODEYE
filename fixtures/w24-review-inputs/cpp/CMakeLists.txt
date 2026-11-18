cmake_minimum_required(VERSION 3.20)
project(ring CXX)
add_executable(ring src/main.cc src/ring.cc)
target_include_directories(ring PRIVATE include)
