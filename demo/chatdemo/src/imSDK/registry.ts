import type WKSDK from "./wksdk";

let instance: WKSDK | null = null;

export const setWKSDKInstance = (sdk: WKSDK) => {
    instance = sdk;
};

export const getWKSDK = (): WKSDK => {
    if (!instance) {
        throw new Error("WKSDK is not initialized yet.");
    }
    return instance;
};
