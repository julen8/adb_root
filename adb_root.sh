#!/bin/sh

echo stop adbd
systemctl stop adbd
sleep 0.5
echo host | tee /sys/devices/platform/soc@0/78d9000.usb/ci_hdrc.0/role

echo 等待 adb 设备连接
adb wait-for-device
echo adb 设备已连接！

echo 重启到 bootloader
adb reboot bootloader

echo 等待 fastboot 设备连接
fastboot wait-for-device 2>/dev/null
echo fastboot 设备已连接！

fastboot oem set-gpu-preemption 0 androidboot.selinux=permissive
fastboot continue
echo 重启到系统
echo success
