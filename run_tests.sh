#! /usr/bin/env bash
# set -eux
# e -> exit on error
# u -> exit on undefined variable
# x -> print command before executing it

VAR=$(go test -list . ./...)
readarray -t y <<<"$VAR"
# Access the elements in the y array (e.g., echo "${y[0]}" for the first line)
# echo "${y[@]}"  # This will print all elements in the array (all test names)

for test_name in "${y[@]}"; do
    # if first cahracter is not a '?' or a 'ok' then run the test
    if [[ ${test_name:0:1} != "?" ]] && [[ ${test_name:0:2} != "ok" ]]; then
        go test "./pkg/kvservice/" -run "^$test_name$" -count=10 # && echo "Test $test_name passed" || echo "Test $test_name failed"
    fi
done