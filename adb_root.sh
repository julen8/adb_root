#!/bin/sh

#https://github.com/HandsomeMod/gc
GADGET_CONTROL=${GADGET_CONTROL:-"/usr/bin/gc"}
USB_ROLE_DEBUG=/sys/class/udc/ci_hdrc.0/device/role
ADB_FUNCTIONFS=/dev/usb-ffs/adb

echo stop adbd
systemctl stop adbd
$GADGET_CONTROL -d
$GADGET_CONTROL -c
sleep 1

echo 切换到 host 模式
echo host > ${USB_ROLE_DEBUG}
sleep 0.2

echo 等待 adb 设备连接
adb wait-for-device
echo adb 设备已连接！

echo 重启到 bootloader
adb reboot bootloader

echo 等待 fastboot 设备连接
fastboot wait-for-device 2>/dev/null
echo fastboot 设备已连接！

echo 开始设置宽容模式
fastboot oem set-gpu-preemption 0 androidboot.selinux=permissive
sleep 0.2

echo 重启到系统
fastboot continue
echo 成功

sleep 0.5

echo 停止 adb
adb kill-server
killall adb

echo 切换到 gadget 模式
echo gadget > ${USB_ROLE_DEBUG}

$GADGET_CONTROL -d
$GADGET_CONTROL -c
$GADGET_CONTROL -a ffs

mkdir -p "$ADB_FUNCTIONFS"
if ! grep -qs " $ADB_FUNCTIONFS " /proc/mounts ; then
    mount -t functionfs adb "$ADB_FUNCTIONFS"
fi

echo 启动 adbd
systemctl restart adbd
sleep 5
$GADGET_CONTROL -e
echo 切换完成
